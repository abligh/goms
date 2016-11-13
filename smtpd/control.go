package smtpd

import (
	"context"
	//	"github.com/sevlyar/go-daemon"
	"github.com/abligh/go-daemon"
	"io"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sync"
	"syscall"
)

// Control mediates the running of the main process
type Control struct {
	quit chan struct{}
	wg   sync.WaitGroup
}

// Startserver starts a single server.
//
// A parent context is given in which the listener runs, as well as a session context in which the sessions (connections) themselves run.
// This enables the sessions to be retained when the listener is cancelled on a SIGHUP
func StartServer(parentCtx context.Context, sessionParentCtx context.Context, sessionWaitGroup *sync.WaitGroup, logger *log.Logger, s ServerConfig) {
	ctx, cancelFunc := context.WithCancel(parentCtx)

	defer func() {
		cancelFunc()
		logger.Printf("[INFO] Stopping server %s:%s", s.Protocol, s.Address)
	}()

	logger.Printf("[INFO] Starting server %s:%s", s.Protocol, s.Address)

	if l, err := NewListener(logger, s); err != nil {
		logger.Printf("[ERROR] Could not create listener for %s:%s: %v", s.Protocol, s.Address, err)
	} else {
		l.Listen(ctx, sessionParentCtx, sessionWaitGroup)
	}
}

// RunConfig - this is effectively the main entry point of the program
//
// We parse the config, then start each of the listeners, restarting them when we get SIGHUP, but being sure not to kill the sessions
func RunConfig(control *Control) {
	// just until we read the configuration
	logger := log.New(os.Stderr, "goms:", log.LstdFlags)
	var logCloser io.Closer
	var sessionWaitGroup sync.WaitGroup
	ctx, cancelFunc := context.WithCancel(context.Background())
	defer func() {
		logger.Println("[INFO] Shutting down")
		cancelFunc()
		sessionWaitGroup.Wait()
		logger.Println("[INFO] Shutdown complete")
		if logCloser != nil {
			logCloser.Close()
		}
		control.wg.Done()
	}()

	intr := make(chan os.Signal, 1)
	term := make(chan os.Signal, 1)
	hup := make(chan os.Signal, 1)
	usr1 := make(chan os.Signal, 1)
	defer close(intr)
	defer close(term)
	defer close(hup)
	defer close(usr1)
	if !*foreground {
		signal.Notify(intr, os.Interrupt)
		signal.Notify(term, syscall.SIGTERM)
		signal.Notify(hup, syscall.SIGHUP)
	}

	signal.Notify(usr1, syscall.SIGUSR1)
	go func() {
		for {
			select {
			case _, ok := <-usr1:
				if !ok {
					return
				}
				logger.Println("[INFO] Run GC()")
				runtime.GC()
				logger.Println("[INFO] GC() done")
				debug.FreeOSMemory()
				logger.Println("[INFO] FreeOsMemory() done")
			}
		}
	}()

	for {
		var wg sync.WaitGroup
		configCtx, configCancelFunc := context.WithCancel(ctx)
		if c, err := ParseConfig(); err != nil {
			logger.Println("[ERROR] Cannot parse configuration file: %v", err)
			return
		} else {
			if nlogger, nlogCloser, err := c.GetLogger(); err != nil {
				logger.Println("[ERROR] Could not load logger: %v", err)
			} else {
				if logCloser != nil {
					logCloser.Close()
				}
				logger = nlogger
				logCloser = nlogCloser
			}
			logger.Printf("[INFO] Loaded configuration.")
			for _, s := range c.Servers {
				s := s // localise loop variable
				go func() {
					wg.Add(1)
					StartServer(configCtx, ctx, &sessionWaitGroup, logger, s)
					wg.Done()
				}()
			}

			select {
			case <-ctx.Done():
				logger.Println("[INFO] Interrupted")
				return
			case <-intr:
				logger.Println("[INFO] Interrupt signal received")
				return
			case <-term:
				logger.Println("[INFO] Terminate signal received")
				return
			case <-control.quit:
				logger.Println("[INFO] Programmatic quit received")
				return
			case <-hup:
				logger.Println("[INFO] Reload signal received; reloading configuration which will be effective for new connections")
				configCancelFunc() // kill the listeners but not the sessions
				wg.Wait()
			}
		}
	}
}

func Run(control *Control) {
	if control == nil {
		control = &Control{}
		// normally adding to a waitgroup inside the go-routine that
		// exits is racy, but nil is only ever passed in if we don't
		// care wat happens on quit
		control.wg.Add(1)
	}

	if *pprof {
		runtime.MemProfileRate = 1
		go http.ListenAndServe(":8080", nil)
	}

	// Just for this routine
	logger := log.New(os.Stderr, "goms:", log.LstdFlags)

	daemon.AddFlag(daemon.StringFlag(sendSignal, "stop"), syscall.SIGTERM)
	daemon.AddFlag(daemon.StringFlag(sendSignal, "reload"), syscall.SIGHUP)

	if daemon.WasReborn() {
		if val := os.Getenv(ENV_CONFFILE); val != "" {
			*configFile = val
		}
		if val := os.Getenv(ENV_PIDFILE); val != "" {
			*pidFile = val
		}
	}

	var err error
	if *configFile, err = filepath.Abs(*configFile); err != nil {
		logger.Fatalf("[CRIT] Error canonicalising config file path: %s", err)
	}
	if *pidFile, err = filepath.Abs(*pidFile); err != nil {
		logger.Fatalf("[CRIT] Error canonicalising pid file path: %v", err)
	}

	// check the configuration parses. We do nothing with this at this stage
	// but it eliminates a problem where the log of the configuration failing
	// is invisible when daemonizing naively (e.g. when no alternate log
	// destination is supplied) and the config file cannot be read
	if _, err := ParseConfig(); err != nil {
		logger.Fatalf("[CRIT] Cannot parse configuration file: %v", err)
	}

	if *foreground {
		RunConfig(control)
		return
	}

	os.Setenv(ENV_CONFFILE, *configFile)
	os.Setenv(ENV_PIDFILE, *pidFile)

	// Define daemon context
	d := &daemon.Context{
		PidFileName: *pidFile,
		PidFilePerm: 0644,
		Umask:       027,
	}

	// Send commands if needed
	if len(daemon.ActiveFlags()) > 0 {
		p, err := d.Search()
		if err != nil {
			logger.Fatalf("[CRIT] Unable send signal to the daemon - not running")
		}
		if err := p.Signal(syscall.Signal(0)); err != nil {
			logger.Fatalf("[CRIT] Unable send signal to the daemon - not running, perhaps PID file is stale")
		}
		daemon.SendCommands(p)
		return
	}

	if !daemon.WasReborn() {
		if p, err := d.Search(); err == nil {
			if err := p.Signal(syscall.Signal(0)); err == nil {
				logger.Fatalf("[CRIT] Daemon is already running (pid %d)", p.Pid)
			} else {
				logger.Printf("[INFO] Removing stale PID file %s", *pidFile)
				os.Remove(*pidFile)
			}
		}
	}

	// Process daemon operations - send signal if present flag or daemonize
	child, err := d.Reborn()
	if err != nil {
		logger.Fatalf("[CRIT] Daemonize: %s", err)
	}
	if child != nil {
		return
	}

	defer func() {
		d.Release()
	}()

	RunConfig(control)
	return
}
