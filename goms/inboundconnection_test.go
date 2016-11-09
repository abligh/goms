package goms

import (
	"bytes"
	"context"
	"log"
	"net"
	"net/smtp"
	"testing"
	"time"
)

// This can be used as the destination for a logger and it'll
// map them into calls to testing.T.Log, so that you only see
// the logging for failed tests.
type testLoggerAdapter struct {
	t      *testing.T
	prefix string
}

func (a *testLoggerAdapter) Write(d []byte) (int, error) {
	if d[len(d)-1] == '\n' {
		d = d[:len(d)-1]
	}
	if a.prefix != "" {
		l := a.prefix + ": " + string(d)
		a.t.Log(l)
		return len(l), nil
	} else {
		a.t.Log(string(d))
		return len(d), nil
	}
}

func newTestLogger(t *testing.T) *log.Logger {
	return log.New(&testLoggerAdapter{t: t}, "", log.Lmicroseconds)
}

func newTestLoggerWithPrefix(t *testing.T, prefix string) *log.Logger {
	return log.New(&testLoggerAdapter{t: t, prefix: prefix}, "", log.Lmicroseconds)
}

// TestITP is an InboundTransactionProcessor which accepts all mail and dumps it
type TestITP struct {
	r    *ICResponse // response to return for all transactions
	err  error       // error to return for all transactions
	data []byte      // captured data
}

// CheckConnection returns the stored response and error
func (i *TestITP) CheckConnection(ctx context.Context, c *InboundConnection) (*ICResponse, error) {
	return i.r, i.err
}

// CheckFromAddress returns the stored response and error
func (i *TestITP) CheckFromAddress(ctx context.Context, c *InboundConnection, address *AddressString) (*ICResponse, error) {
	return i.r, i.err
}

// CheckRecipientAddress returns the stored response and error
func (i *TestITP) CheckRecipientAddress(ctx context.Context, c *InboundConnection, address *AddressString) (*ICResponse, error) {
	return i.r, i.err
}

// ProcessMail returns the stored response and error
func (i *TestITP) ProcessMail(ctx context.Context, c *InboundConnection, data []byte) (*ICResponse, error) {
	if (i.r != nil && i.r.IsError()) || i.err != nil {
		return i.r, i.err
	}
	i.data = make([]byte, len(data))
	copy(i.data, data)
	return i.r, nil
}

type TestConnection struct {
	sc      net.Conn
	cc      net.Conn
	ic      *InboundConnection
	ctx     context.Context
	cancel  context.CancelFunc
	client  *smtp.Client
	timeout *time.Timer
	itp     *TestITP
}

func NewTestConnection(t *testing.T) *TestConnection {
	sc, cc := net.Pipe()
	ic, _ := newInboundConnection(nil, newTestLogger(t), sc)
	tc := &TestConnection{
		sc:  sc,
		cc:  cc,
		ic:  ic,
		itp: &TestITP{},
	}
	ic.ITP = tc.itp
	cc.SetDeadline(time.Now().Add(5 * time.Second))

	tc.ctx, tc.cancel = context.WithCancel(context.Background())

	tc.timeout = time.AfterFunc(10*time.Second, func() {
		t.Log("[FATAL] Abort after timeout")
		tc.Close()
	})
	// Start the server
	go ic.Serve(tc.ctx)

	return tc
}

func (tc *TestConnection) Connect() error {
	if client, err := smtp.NewClient(tc.cc, "localhost"); err != nil {
		return err
	} else {
		tc.client = client
		return nil
	}
}

func (tc *TestConnection) Close() error {
	tc.timeout.Stop()
	tc.cancel()
	if tc.client != nil {
		tc.client.Close()
	}
	tc.cc.Close()
	// server connection closed by Serve()
	return nil
}

func TestConnect(t *testing.T) {
	tc := NewTestConnection(t)
	defer tc.Close()

	if err := tc.Connect(); err != nil {
		t.Fatalf("Cannot connect to server: %v", err)
	}

	if err := tc.client.Quit(); err != nil {
		t.Fatal("Cannot send quit to server")
	} else {
		tc.client = nil // don't attempt Close()
	}
}

func TestConnectForbidden(t *testing.T) {
	tc := NewTestConnection(t)
	defer tc.Close()

	tc.itp.r = &ICResponse{
		lines: newICRL(550, "5.5.0 Error: prohibited"),
	}

	if err := tc.Connect(); err == nil {
		t.Fatalf("Can connect to server when should have been prohibited")
	}
}

func TestHello(t *testing.T) {
	tc := NewTestConnection(t)
	defer tc.Close()

	if err := tc.Connect(); err != nil {
		t.Fatalf("Cannot connect to server: %v", err)
	}

	if err := tc.client.Hello("localhost"); err != nil {
		t.Fatalf("Cannot say hello to server: %v", err)
	}

	if err := tc.client.Quit(); err != nil {
		t.Fatal("Cannot send quit to server")
	} else {
		tc.client = nil // don't attempt Close()
	}
}

func TestAddressingSequencing(t *testing.T) {
	tc := NewTestConnection(t)
	defer tc.Close()

	if err := tc.Connect(); err != nil {
		t.Fatalf("Cannot connect to server: %v", err)
	}

	if err := tc.client.Hello("localhost"); err != nil {
		t.Fatalf("Cannot execute EHLO: %v", err)
	}

	if err := tc.client.Rcpt("a@b"); err == nil {
		t.Fatalf("Accepted 'RCPT TO' before MAIL")
	}

	if err := tc.client.Mail("a@b"); err != nil {
		t.Fatalf("Cannot execute 'MAIL FROM' to server: %v", err)
	}

	if err := tc.client.Mail("a@b"); err == nil {
		t.Fatalf("Accepted second 'MAIL FROM'")
	}

	if err := tc.client.Rcpt("a@b"); err != nil {
		t.Fatalf("Cannot execute 'RCPT TO': %v", err)
	}

	if err := tc.client.Rcpt("aa"); err == nil {
		t.Fatalf("Incorrectly executed bad 'RCPT TO'")
	}

	tc.itp.r = &ICResponse{
		lines: newICRL(550, "5.5.0 Error: prohibited"),
	}
	if err := tc.client.Rcpt("a@a"); err == nil {
		t.Fatalf("Incorrectly executed prohibited 'RCPT TO'")
	}
	tc.itp.r = nil

	if err := tc.client.Reset(); err != nil {
		t.Fatalf("Cannot execute RSET: %v", err)
	}

	if err := tc.client.Rcpt("a@b"); err == nil {
		t.Fatalf("RSET appears not to have ended transaction")
	}

	tc.itp.r = &ICResponse{
		lines: newICRL(550, "5.5.0 Error: prohibited"),
	}
	if err := tc.client.Mail("a@b"); err == nil {
		t.Fatalf("Incorrectly executed prohibited 'MAIL FROM' after RSET")
	}
	tc.itp.r = nil

	if err := tc.client.Mail("a@b"); err != nil {
		t.Fatalf("Cannot execute 'MAIL FROM' after RSET: %v", err)
	}

	if err := tc.client.Rcpt("a@b"); err != nil {
		t.Fatalf("Cannot execute 'RCPT TO' after RSET: %v", err)
	}

	if err := tc.client.Quit(); err != nil {
		t.Fatal("Cannot send QUIT: %v", err)
	} else {
		tc.client = nil // don't attempt Close()
	}
}

func TestData(t *testing.T) {
	tc := NewTestConnection(t)
	defer tc.Close()

	if err := tc.Connect(); err != nil {
		t.Fatalf("Cannot connect to server: %v", err)
	}

	if err := tc.client.Hello("localhost"); err != nil {
		t.Fatalf("Cannot execute EHLO: %v", err)
	}

	if err := tc.client.Mail("a@b"); err != nil {
		t.Fatalf("Cannot execute 'MAIL FROM' to server: %v", err)
	}

	if err := tc.client.Rcpt("a@b"); err != nil {
		t.Fatalf("Cannot execute 'RCPT TO': %v", err)
	}

	if writer, err := tc.client.Data(); err != nil {
		t.Fatalf("Cannot execute 'RCPT TO': %v", err)
	} else {
		// do not put broken line endings in here (e.g. \n rather than \r\n) and ensure you end with a \r, as otherwise
		// golang's smtp sender fixes them up
		towrite := []byte("Subject: test\r\n\r\nA line\r\n\r\n.begins with a dot\r\n\r\n.\r\nmore\r\nthat's all folks!\r\n")
		if n, err := writer.Write(towrite); err != nil || n != len(towrite) {
			t.Fatalf("Write failed err=%v len=%d (expecting %d)", err, n, len(towrite))
		}
		if err := writer.Close(); err != nil {
			t.Fatalf("Close failed: %v", err)
		}
		if !bytes.Equal(tc.itp.data, towrite) {
			t.Fatalf("Written data not identical")
		}
	}

	if err := tc.client.Quit(); err != nil {
		t.Fatal("Cannot send QUIT: %v", err)
	} else {
		tc.client = nil // don't attempt Close()
	}
}
