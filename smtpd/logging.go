package smtpd

import (
	"fmt"
	"io"
	"log"
	"log/syslog"
	_ "net/http/pprof"
	"os"
	"regexp"
	"strconv"
)

// LogConfig specifies configuration for logging
type LogConfig struct {
	File           string // a file to log to
	FileMode       string // file mode
	SyslogFacility string // a syslog facility name - set to enable syslog
	Date           bool   // log the date - i.e. log.Ldate
	Time           bool   // log the time - i.e. log.Ltime
	Microseconds   bool   // log microseconds - i.e. log.Lmicroseconds
	UTC            bool   // log time in URC - i.e. LUTC
	SourceFile     bool   // log source file - i.e. Lshortfile
}

// SyslogWriter is a WriterCloser that logs to syslog with an extracted priority
type SyslogWriter struct {
	facility syslog.Priority
	w        *syslog.Writer
}

// facilityMap maps textual
var facilityMap map[string]syslog.Priority = map[string]syslog.Priority{
	"kern":     syslog.LOG_KERN,
	"user":     syslog.LOG_USER,
	"mail":     syslog.LOG_MAIL,
	"daemon":   syslog.LOG_DAEMON,
	"auth":     syslog.LOG_AUTH,
	"syslog":   syslog.LOG_SYSLOG,
	"lpr":      syslog.LOG_LPR,
	"news":     syslog.LOG_NEWS,
	"uucp":     syslog.LOG_UUCP,
	"cron":     syslog.LOG_CRON,
	"authpriv": syslog.LOG_AUTHPRIV,
	"ftp":      syslog.LOG_FTP,
	"local0":   syslog.LOG_LOCAL0,
	"local1":   syslog.LOG_LOCAL1,
	"local2":   syslog.LOG_LOCAL2,
	"local3":   syslog.LOG_LOCAL3,
	"local4":   syslog.LOG_LOCAL4,
	"local5":   syslog.LOG_LOCAL5,
	"local6":   syslog.LOG_LOCAL6,
	"local7":   syslog.LOG_LOCAL7,
}

// levelMap maps textual levels to syslog levels
var levelMap map[string]syslog.Priority = map[string]syslog.Priority{
	"EMERG":   syslog.LOG_EMERG,
	"ALERT":   syslog.LOG_ALERT,
	"CRIT":    syslog.LOG_CRIT,
	"ERR":     syslog.LOG_ERR,
	"ERROR":   syslog.LOG_ERR,
	"WARN":    syslog.LOG_WARNING,
	"WARNING": syslog.LOG_WARNING,
	"NOTICE":  syslog.LOG_NOTICE,
	"INFO":    syslog.LOG_INFO,
	"DEBUG":   syslog.LOG_DEBUG,
}

// Create a new syslog writer
func NewSyslogWriter(facility string) (*SyslogWriter, error) {
	f := syslog.LOG_DAEMON
	if ff, ok := facilityMap[facility]; ok {
		f = ff
	}

	if w, err := syslog.New(f|syslog.LOG_INFO, "goms:"); err != nil {
		return nil, err
	} else {
		return &SyslogWriter{
			w: w,
		}, nil
	}
}

// Close the channel
func (s *SyslogWriter) Close() error {
	return s.w.Close()
}

var deletePrefix *regexp.Regexp = regexp.MustCompile("goms:")
var replaceLevel *regexp.Regexp = regexp.MustCompile("\\[[A-Z]+\\] ")

// Write to the syslog, removing the prefix and setting the appropriate level
func (s *SyslogWriter) Write(p []byte) (n int, err error) {
	p1 := deletePrefix.ReplaceAllString(string(p), "")
	level := ""
	tolog := string(replaceLevel.ReplaceAllStringFunc(p1, func(l string) string {
		level = l
		return ""
	}))
	switch level {
	case "[DEBUG] ":
		s.w.Debug(tolog)
	case "[INFO] ":
		s.w.Info(tolog)
	case "[NOTICE] ":
		s.w.Notice(tolog)
	case "[WARNING] ", "[WARN] ":
		s.w.Warning(tolog)
	case "[ERROR] ", "[ERR] ":
		s.w.Err(tolog)
	case "[CRIT] ":
		s.w.Crit(tolog)
	case "[ALERT] ":
		s.w.Alert(tolog)
	case "[EMERG] ":
		s.w.Emerg(tolog)
	default:
		s.w.Notice(tolog)
	}
	return len(p), nil
}

func (c *Config) GetLogger() (*log.Logger, io.Closer, error) {
	logFlags := 0
	if c.Logging.Date {
		logFlags |= log.Ldate
	}
	if c.Logging.Time {
		logFlags |= log.Ltime
	}
	if c.Logging.Microseconds {
		logFlags |= log.Lmicroseconds
	}
	if c.Logging.SourceFile {
		logFlags |= log.Lshortfile
	}
	if c.Logging.File != "" {
		mode := os.FileMode(0644)
		if c.Logging.FileMode != "" {
			if i, err := strconv.ParseInt(c.Logging.FileMode, 8, 32); err != nil {
				return nil, nil, fmt.Errorf("Cannot read file logging mode: %v", err)
			} else {
				mode = os.FileMode(i)
			}
		}
		if file, err := os.OpenFile(c.Logging.File, os.O_CREATE|os.O_APPEND|os.O_WRONLY, mode); err != nil {
			return nil, nil, err
		} else {
			return log.New(file, "goms:", logFlags), file, nil
		}
	}
	if c.Logging.SyslogFacility != "" {
		if s, err := NewSyslogWriter(c.Logging.SyslogFacility); err != nil {
			return nil, nil, err
		} else {
			return log.New(s, "goms:", logFlags), s, nil
		}
	} else {
		return log.New(os.Stderr, "goms:", logFlags), nil, nil
	}
}
