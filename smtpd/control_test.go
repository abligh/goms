package smtpd

import (
	"flag"
	"fmt"
	"io/ioutil"
	"net"
	"net/smtp"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var config string = `
servers:
- protocol: tcp
  address: 127.0.0.1:30025
logging:
  syslogfacility: local1
`

func sendTestMail(t *testing.T) {

	conn, err := net.DialTimeout("tcp", "127.0.0.1:30025", 2*time.Second)
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
	if err := ioutil.WriteFile(conffn, []byte(config), 0666); err != nil {
		t.Fatalf("Could not create config file: %v", err)
	}
	pidfn := filepath.Join(dir, "goms.pid")

	flagParse([]string{"goms", "-c", conffn, "-p", pidfn})
	Run(nil)

	waitForPidFile(t, pidfn, true)

	time.Sleep(100 * time.Millisecond)

	sendTestMail(t)

	time.Sleep(100 * time.Millisecond)
	os.Args = []string{"goms", "-c", conffn, "-p", pidfn, "-s", "reload"}
	flag.Parse()
	Run(nil)

	waitForPidFile(t, pidfn, true)

	time.Sleep(20 * time.Millisecond)

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
	if err := ioutil.WriteFile(conffn, []byte(config), 0666); err != nil {
		t.Fatalf("Could not create config file: %v", err)
	}
	pidfn := filepath.Join(dir, "goms.pid")

	flagParse([]string{"goms", "-c", conffn, "-p", pidfn, "-f"})
	c := &Control{
		quit: make(chan struct{}),
	}
	c.wg.Add(1)
	go Run(c)

	time.Sleep(200 * time.Millisecond)

	sendTestMail(t)
	close(c.quit)
	c.wg.Wait()
}
