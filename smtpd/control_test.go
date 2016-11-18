package smtpd

import (
	"flag"
	"fmt"
	"io/ioutil"
	"net"
	"net/smtp"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

var controlTestConfig string = `
servers:
- protocol: tcp
  address: 127.0.0.1:30025
logging:
  syslogfacility: local1
`

const (
	gomsfgaction = "GOMS_FG_ACTION"
)

func sendTestMail(t *testing.T) {

	var conn net.Conn
	var err error
	retries := 0

	for retries < 20 {
		conn, err = net.DialTimeout("tcp", "127.0.0.1:30025", 2*time.Second)
		if err == nil {
			break
		}
		retries++
		time.Sleep(100 * time.Millisecond)
	}

	if err != nil {
		t.Fatalf("Could not dial to initiate connection: %v", err)
	}

	c, err := smtp.NewClient(conn, "localhost")
	// Connect to the local SMTP server.
	// c, err := smtp.Dial("127.0.0.1:30025")
	if err != nil {
		t.Fatalf("Could not connect to local SMTP server: %v", err)
	}

	timeout := time.AfterFunc(10*time.Second, func() {
		t.Log("[FATAL] Abort after timeout")
		c.Close()
	})
	defer timeout.Stop()

	if err := c.Mail("sender@example.org"); err != nil {
		t.Fatalf("Could not send MAIL: %v", err)
	}

	if err := c.Rcpt("recipient@example.net"); err != nil {
		t.Fatalf("Could not send RCPT: %v", err)
	}

	// Send the email body.
	wc, err := c.Data()
	if err != nil {
		t.Fatalf("Could not send DATA: %v", err)
	}
	_, err = fmt.Fprintf(wc, "This is the email body")
	if err != nil {
		t.Fatalf("Could not send body: %v", err)
	}
	if err = wc.Close(); err != nil {
		t.Fatalf("Could not close mail transaction: %v", err)
	}
	// Send the QUIT command and close the connection.
	if err = c.Quit(); err != nil {
		t.Fatalf("Could not send QUIT: %v", err)
	}
}

func flagParse(args []string) {
	saveArgs := os.Args
	os.Args = args
	flag.Parse()
	os.Args = saveArgs
}

func waitForPidFile(t *testing.T, pidfn string, shouldExist bool) {
	correct := false
	for i := 1; i < 20; i++ {
		if _, err := os.Stat(pidfn); shouldExist == (err == nil || !os.IsNotExist(err)) {
			correct = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !correct {
		if shouldExist {
			t.Fatalf("Pidfile not present: %v", pidfn)
		} else {
			t.Fatalf("Pidfile not deleted: %v", pidfn)
		}
	}
}

// this test needs to be first
func TestDaemonize(t *testing.T) {
	dir, err := ioutil.TempDir("", "gomstest")
	if err != nil {
		t.Fatalf("Could not create temporary directory: %v", err)
	}
	defer os.RemoveAll(dir)

	conffn := filepath.Join(dir, "goms.conf")
	if err := ioutil.WriteFile(conffn, []byte(controlTestConfig), 0666); err != nil {
		t.Fatalf("Could not create config file: %v", err)
	}
	pidfn := filepath.Join(dir, "goms.pid")

	flagParse([]string{"goms", "-c", conffn, "-p", pidfn})
	Run(nil)

	waitForPidFile(t, pidfn, true)

	time.Sleep(100 * time.Millisecond)

	sendTestMail(t)

	time.Sleep(100 * time.Millisecond)
	flagParse([]string{"goms", "-c", conffn, "-p", pidfn, "-s", "reload"})
	Run(nil)

	waitForPidFile(t, pidfn, true)

	time.Sleep(100 * time.Millisecond)

	sendTestMail(t)

	time.Sleep(100 * time.Millisecond)
	flagParse([]string{"goms", "-c", conffn, "-p", pidfn, "-s", "stop", "-test.v", "-test.run", "TestDaeemonize"})
	Run(nil)

	waitForPidFile(t, pidfn, false)
}

func TestForeground(t *testing.T) {
	dir, err := ioutil.TempDir("", "gomstest")
	if err != nil {
		t.Fatalf("Could not create temporary directory: %v", err)
	}
	defer os.RemoveAll(dir)

	conffn := filepath.Join(dir, "goms.conf")
	if err := ioutil.WriteFile(conffn, []byte(controlTestConfig), 0666); err != nil {
		t.Fatalf("Could not create config file: %v", err)
	}
	pidfn := filepath.Join(dir, "goms.pid")

	c := &Control{
		quit:     make(chan struct{}),
		dummyRun: true,
	}
	c.wg.Add(1)

	switch os.Getenv(gomsfgaction) {
	case "signalnotrunning":
		flagParse([]string{"goms", "-c", conffn, "-p", pidfn, "-s", "reload"})
	case "signalunknown":
		flagParse([]string{"goms", "-c", conffn, "-p", pidfn, "-s", "unknown"})
	case "badconffile":
		flagParse([]string{"goms", "-c", "////", "-p", pidfn, "-f"})
	case "badpidfile":
		flagParse([]string{"goms", "-c", conffn, "-p", "////"})
	case "noconffile":
		flagParse([]string{"goms", "-c", conffn + "-unknown", "-p", pidfn, "-f"})
	default:
		flagParse([]string{"goms", "-c", conffn, "-p", pidfn, "-f"})
		c.dummyRun = false
	}

	go Run(c)

	time.Sleep(200 * time.Millisecond)

	if c.dummyRun {
		os.Exit(0)
	}

	sendTestMail(t)
	close(c.quit)
	c.wg.Wait()
}

func testForegroundAction(t *testing.T, action string) {
	cmd := exec.Command(os.Args[0], "-test.run=TestForeground")
	cmd.Env = append(os.Environ(), fmt.Sprintf("%s=%s", gomsfgaction, action))
	err := cmd.Run()
	if e, ok := err.(*exec.ExitError); ok && !e.Success() {
		return
	}
	t.Fatalf("TestLaunchErrors test '%s' ran with err %v, want exit status 1", action, err)
}

func TestLaunchErrors(t *testing.T) {
	testForegroundAction(t, "signalnotrunning")
	// testForegroundAction(t, "signalunknown")
	testForegroundAction(t, "badconffile")
	testForegroundAction(t, "badpidfile")
	testForegroundAction(t, "noconffile")
}
