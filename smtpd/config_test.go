package smtpd

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
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

	os.Args = []string{"goms", "-c", conffn, "-p", pidfn, "-f"}
	flag.Parse()
	go Run(nil)

	time.Sleep(200 * time.Millisecond)

	sendTestMail(t)
}

func skipTestDaemonize(t *testing.T) {
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

	os.Args = []string{"goms", "-c", conffn, "-p", pidfn}
	flag.Parse()
	Run(nil)

	time.Sleep(500 * time.Millisecond)

	log.Printf(">>>> it's at %v", pidfn)
	if pid, err := ioutil.ReadFile(pidfn); err == nil {
		log.Printf(">>> pid is %v", pid)
	} else {
		log.Printf(">>> Error: %v", err)
	}

	if _, err := os.Stat(pidfn); err != nil {
		t.Fatalf("Could not find pidfile: %v", err)
	}

	sendTestMail(t)

	time.Sleep(100 * time.Millisecond)
	os.Args = []string{"goms", "-c", conffn, "-p", pidfn, "-s", "reload"}
	flag.Parse()
	Run(nil)

	time.Sleep(100 * time.Millisecond)

	if _, err := os.Stat(pidfn); err != nil {
		t.Fatalf("Could not find pidfile: %v", err)
	}

	sendTestMail(t)

	time.Sleep(100 * time.Millisecond)
	os.Args = []string{"goms", "-c", conffn, "-p", pidfn, "-s", "stop"}
	flag.Parse()
	Run(nil)

	exists := true
	for i := 1; i < 20; i++ {
		if _, err := os.Stat(pidfn); err != nil && os.IsNotExist(err) {
			exists = false
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if exists {
		t.Fatalf("Pidfile not deleted: %v", pidfn)
	}
}
