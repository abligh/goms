package goms

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"log"
	"net"
	"regexp"
	"strings"
	"time"
)

const (
	maxUnrecognisedCommands = 20 // this normally indicates SMTP has got out sync
)

// InboundTransactionProcessor is an interface representing an inbound transaction processor, i.e.
// structs that satisfy this interface can check inbound SMTP connections and process their data
type InboundTransactionProcessor interface {
	CheckConnection(ctx context.Context, c *InboundConnection) (*ICResponse, error)
	CheckFromAddress(ctx context.Context, c *InboundConnection, address *AddressString) (*ICResponse, error)
	CheckRecipientAddress(ctx context.Context, c *InboundConnection, address *AddressString) (*ICResponse, error)
	ProcessMail(ctx context.Context, c *InboundConnection, data []byte) (*ICResponse, error)
}

// DummyITP is an InboundTransactionProcessor which accepts all mail and dumps it
type DummyITP struct{}

// CheckConnection accepts all servers
func (i *DummyITP) CheckConnection(ctx context.Context, c *InboundConnection) (*ICResponse, error) {
	return nil, nil
}

// CheckFromAddress accepts all from addresses
func (i *DummyITP) CheckFromAddress(ctx context.Context, c *InboundConnection, address *AddressString) (*ICResponse, error) {
	return nil, nil
}

// CheckRecipientAddress accepts all recipient addresses
func (i *DummyITP) CheckRecipientAddress(ctx context.Context, c *InboundConnection, address *AddressString) (*ICResponse, error) {
	return nil, nil
}

// ProcessMail accepts all mail and does nothing with it
func (i *DummyITP) ProcessMail(ctx context.Context, c *InboundConnection, data []byte) (*ICResponse, error) {
	return nil, nil
}

// ConnectionParameters holds parameters for each inbound connection
type InboundConnectionParameters struct {
	IdleTimeout        time.Duration // time to shut connection if idle
	ReadTimeout        time.Duration // time to read other than at command stage
	WriteTimeout       time.Duration // time to write
	GreetingHostname   string
	GreetingMailserver string
	MaxMessageSize     int
}

// Connection holds the details for each connection
type InboundConnection struct {
	params               *InboundConnectionParameters // parameters
	conn                 net.Conn                     // the connection that is used as the SMTP transport
	plainConn            net.Conn                     // the unencrypted (original) connection
	tlsConn              net.Conn                     // the TLS encrypted connection
	logger               *log.Logger                  // a logger
	listener             *Listener                    // the listener than invoked us
	name                 string                       // the name of the connection for logging purposes
	rd                   *bufio.Reader                // buffered reader
	wr                   *bufio.Writer                // buffered writer
	rdwr                 *bufio.ReadWriter            // composite read writer
	needsFlush           bool                         // if we've skipped a flush due to pipelining mode
	unrecognisedCommands int                          // Number of unrecognised commands so far
	RecipientList        []*AddressString             // current recipient list
	inTransaction        bool                         // true if in a transaction (i.e. has had 'MAIL FROM')
	ReversePath          AddressString                // current sender
	ITP                  InboundTransactionProcessor  // inbound transaction processor associated with this connection
}

// ICCommand holds an inbound command
type ICCommand struct {
	buf     []byte
	invalid bool
}

// AddressString holds an email address. Subtyped so we can play with it later
type AddressString string

// ICResponseLine is a single line SMTP response code
type ICResponseLine struct {
	code int    // The integer response code
	text string // the textual response
}

// ICResponse is a potentially multiline SMTP response
type ICResponse struct {
	lines       []ICResponseLine // The response lines
	final       bool             // should the connection be closed after sending
	canPipeline bool             // if we can skip a flush in pipelining mode
}

// newICRL creates a new slice of response lines with consisting of one entry
// made from the code and text specified
func newICRL(code int, text string) []ICResponseLine {
	return []ICResponseLine{ICResponseLine{code: code, text: text}}
}

// addICRL adds a new line to an existing response code
func (r *ICResponse) addICRL(code int, text string) {
	r.lines = append(r.lines, ICResponseLine{code: code, text: text})
}

// IsError() returns true if and only if r is an error code (i.e. 400 to 599)
// Technically there is a response code on each line of a multiline response, but
// we assume these all have the same code
func (r *ICResponse) IsError() bool {
	if len(r.lines) == 0 {
		return false
	}
	return r.lines[0].code >= 400 && r.lines[0].code <= 599
}

// inboundRE is a regexp used to canonicalise addresses and strip source routing
var (
	inboundRE = regexp.MustCompile(`^([^:]+:)?([^@:]+)@([^@:]+)$`)
)

// CanonicaliseInboundAddress changes a string containing an email address into
// canonical format and returns it as an AddressString. This currently involves stripping
// source routing information
func CanonicaliseInboundAddress(a string) *AddressString {
	if match := inboundRE.FindStringSubmatch(a); match == nil || len(match) != 4 {
		return nil
	} else {
		as := AddressString(fmt.Sprintf("%s@%s", match[2], strings.ToLower(match[3])))
		return &as
	}
}

// String() returns a string representation of an AddressString
func (as *AddressString) String() string {
	return string(*as)
}

// Verb represents an SMTP verb and the action method associated with it
type Verb struct {
	Run func(c *InboundConnection, ctx context.Context, params []byte) (*ICResponse, error)
}

// reset resets the internal transaction state of a connection
func (c *InboundConnection) reset() {
	c.RecipientList = []*AddressString{}
	c.ReversePath = ""
	c.inTransaction = false
}

// doHELO implements the HELO command
func (c *InboundConnection) doHELO(ctx context.Context, params []byte) (*ICResponse, error) {
	c.reset()
	return &ICResponse{
		lines: newICRL(250, c.params.GreetingHostname),
	}, nil
}

// do EHLO implements the EHLO command
func (c *InboundConnection) doEHLO(ctx context.Context, params []byte) (*ICResponse, error) {
	c.reset()
	r := &ICResponse{
		lines: newICRL(250, c.params.GreetingHostname),
	}
	r.addICRL(250, "PIPELINING")
	//r.addICRL(250, "VRFY")
	//r.addICRL(250, "ETRN")
	r.addICRL(250, "ENHANCEDSTATUSCODES")
	r.addICRL(250, "8BITMIME")
	r.addICRL(250, "SMTPUTF8") // TODO - we may wish to check for this in the MAIL command, but currently unnecessary as we have no UTF8 replies
	r.addICRL(250, fmt.Sprintf("SIZE %d", c.params.MaxMessageSize))
	return r, nil
}

var (
	// despite the RFC, the angle brackets are often ommitted, e.g. by WinCE
	mailFromRE = regexp.MustCompile(`^[Ff][Rr][Oo][Mm]:\s*<?([^<>]*)>?.*`)
)

// doMAIL implements the MAIL command
func (c *InboundConnection) doMAIL(ctx context.Context, params []byte) (*ICResponse, error) {
	if c.inTransaction {
		return &ICResponse{
			//RFC5321 4.4.1
			lines: newICRL(503, "5.5.1 Error: nested MAIL commands"),
		}, nil
	}
	if match := mailFromRE.FindSubmatch(params); match == nil || len(match) != 2 {
		return &ICResponse{
			//RFC5321 3.3
			lines: newICRL(550, "5.1.7 Error: bad envelope sender address format"),
		}, nil
	} else {
		fromAddress := AddressString(match[1])

		// check with the ITP that this is acceptable
		if r, err := c.ITP.CheckFromAddress(ctx, c, &fromAddress); r != nil && r.IsError() || err != nil {
			return r, err
		}

		c.inTransaction = true
		c.ReversePath = fromAddress
		return &ICResponse{
			lines:       newICRL(250, fmt.Sprintf("2.1.0 OK: mail is from '%s'", c.ReversePath)),
			canPipeline: true,
		}, nil
	}
}

var (
	// despite the RFC, the angle brackets are often ommitted, e.g. by WinCE
	rcptToRE = regexp.MustCompile(`^[Tt][Oo]:\s*<?([^<>]*)>?.*`)
)

// doRCPT implements the RCPT command
func (c *InboundConnection) doRCPT(ctx context.Context, params []byte) (*ICResponse, error) {
	if !c.inTransaction {
		return &ICResponse{
			// RFC5321 4.4.1
			lines: newICRL(503, "5.5.1 Error: missing MAIL command before RCPT"),
		}, nil
	}
	if match := rcptToRE.FindSubmatch(params); match == nil || len(match) != 2 {
		return &ICResponse{
			// RFC5321 3.3
			lines: newICRL(550, "5.1.3 Error: bad envelope recepient address component"),
		}, nil
	} else {
		if rcptAddress := CanonicaliseInboundAddress(string(match[1])); rcptAddress == nil {
			return &ICResponse{
				// RFC5321 3.3
				lines: newICRL(550, "5.1.3 Error: bad envelope recepient address format"),
			}, nil
		} else {
			// check with the ITP that this is acceptable
			if r, err := c.ITP.CheckRecipientAddress(ctx, c, rcptAddress); r != nil && r.IsError() || err != nil {
				return r, err
			}

			c.RecipientList = append(c.RecipientList, rcptAddress)
			return &ICResponse{
				lines:       newICRL(250, fmt.Sprintf("2.1.5 OK: mail recipient '%s'", rcptAddress.String())),
				canPipeline: true,
			}, nil
		}
	}
}

// doDATA implements the DATA command
func (c *InboundConnection) doDATA(ctx context.Context, params []byte) (*ICResponse, error) {
	if !c.inTransaction {
		return &ICResponse{
			// RFC5321 4.4.1
			lines: newICRL(503, "5.5.1 Error: missing MAIL command before DATA"),
		}, nil
	}
	if len(c.RecipientList) == 0 {
		return &ICResponse{
			// RFC5321 3.3
			lines: newICRL(553, "5.5.1 Error: no valid recipients"),
		}, nil
	}

	ready := &ICResponse{
		lines: newICRL(354, "354 End data with <CR><LF>.<CR><LF>"),
	}

	// This performs a flush too
	if err := c.Send(ready); err != nil {
		return nil, err
	}

	// on exit we have now lost our transaction
	defer c.reset()

	// perhaps we should textproto/DotReader with some form of LimitReader

	var body bytes.Buffer
	startOfLine := true
	oversize := false
	crlf := []byte("\r\n")

	for {
		// TODO: add total message timeout too, to stop sloris attack
		c.conn.SetDeadline(time.Now().Add(c.params.ReadTimeout))
		buf, err := c.rdwr.ReadSlice('\n')
		if err != nil {
			// buf may be non-empty, but that's OK as we're throwing it away anyway
			return nil, err
		}

		if len(buf) == 0 {
			continue
		}
		// if this just ends with a \n (not a \r\n) we just concatenate and continue
		// as we don't need to check for line endings. Per RFC5321 s 4.1.1.4
		// <LF>.<LF> is not a terminator

		lineStartsWithDot := buf[0] == '.' && startOfLine
		if lineStartsWithDot {
			buf = buf[1:]
		}

		// Allow some lee-way here. We do an exact check below
		// We politely swallow oversize messages, but don't actually queue them
		if !oversize && len(buf)+body.Len() > c.params.MaxMessageSize+1024 {
			oversize = true
			// release memory early
			body.Reset()
		}

		if !bytes.HasSuffix(buf, crlf) {
			if !oversize {
				body.Write(buf)
			}
			startOfLine = false
			continue
		}

		// Now we know we have got something ending in \r\n.
		// We thus can check for a terminator. Our dot will have been removed, so we
		// need to look for startOfLine (else something earlier ending in \n but not \r\n
		// has passed), AND lineStartsWithDot AND the buffer is '\r\n' AND either the existing
		// buffer is either empty (dot on first line, which is illegal for other reasons
		// like no trace information, but we need to treat at this level as ending the
		// transaction), or ends with \r\n

		terminator := startOfLine && lineStartsWithDot && len(buf) == len(crlf) &&
			(bytes.HasSuffix(body.Bytes(), crlf) || body.Len() == 0)

		if !terminator {
			if !oversize {
				body.Write(buf)
			}
			startOfLine = true
			continue
		}

		// We don't add the (dropped) dot, or the final CRLF
		break
	}

	// reject messages we have truncated, and any strictly oversize messages
	if oversize || body.Len() > c.params.MaxMessageSize {
		return &ICResponse{
			// RFC5321 4.5.3.1.9
			lines: newICRL(552, "4.3.4 Error: message too big for system"),
		}, nil
	}

	// now we need to do something with the message.
	log.Printf("[DEBUG] message = %v", body.Bytes())

	// Process via the ITP. Note this can return its own 250 message, with the appropriate 'queued' response
	// (e.g. a queue ID), which is more helpful than the default message
	if r, err := c.ITP.ProcessMail(ctx, c, body.Bytes()); r != nil || err != nil {
		return r, err
	}

	return &ICResponse{
		lines: newICRL(250, "2.0.0 OK: queued (ID unknown)"),
	}, nil
}

// doRSET implements the RSET command
func (c *InboundConnection) doRSET(ctx context.Context, params []byte) (*ICResponse, error) {
	c.reset()
	return &ICResponse{
		lines:       newICRL(250, "2.0.0 OK"),
		canPipeline: true,
	}, nil
}

// doVRFY implements the VRFY command
func (c *InboundConnection) doVRFY(ctx context.Context, params []byte) (*ICResponse, error) {
	return &ICResponse{
		lines:       newICRL(502, "5.5.1 Error: command not implemented"),
		canPipeline: true,
	}, nil
}

// doEXPN implements the EXPN command
func (c *InboundConnection) doEXPN(ctx context.Context, params []byte) (*ICResponse, error) {
	return &ICResponse{
		lines:       newICRL(502, "5.5.1 Error: command not implemented"),
		canPipeline: true,
	}, nil
}

// doHELP implements the HELP command
func (c *InboundConnection) doHELP(ctx context.Context, params []byte) (*ICResponse, error) {
	return &ICResponse{
		lines: newICRL(250, "2.0.0 OK: but I currently have no help to give"),
	}, nil
}

// doNOOP implements the NOOP command - oddly not pipelineable
func (c *InboundConnection) doNOOP(ctx context.Context, params []byte) (*ICResponse, error) {
	return &ICResponse{
		lines: newICRL(250, "2.0.0 OK"),
	}, nil
}

// doQUIT implements the QUIT command
func (c *InboundConnection) doQUIT(ctx context.Context, params []byte) (*ICResponse, error) {
	c.reset()
	return &ICResponse{
		lines: newICRL(221, "2.0.0 Bye"),
		final: true,
	}, nil
}

// verbs is a map of SMTP verbs to the handlers they use
var verbs map[string]Verb = map[string]Verb{
	"HELO": Verb{Run: (*InboundConnection).doHELO},
	"EHLO": Verb{Run: (*InboundConnection).doEHLO},
	"MAIL": Verb{Run: (*InboundConnection).doMAIL},
	"RCPT": Verb{Run: (*InboundConnection).doRCPT},
	"DATA": Verb{Run: (*InboundConnection).doDATA},
	"RSET": Verb{Run: (*InboundConnection).doRSET},
	"VRFY": Verb{Run: (*InboundConnection).doVRFY},
	"EXPN": Verb{Run: (*InboundConnection).doEXPN},
	"HELP": Verb{Run: (*InboundConnection).doHELP},
	"NOOP": Verb{Run: (*InboundConnection).doNOOP},
	"QUIT": Verb{Run: (*InboundConnection).doQUIT},
}

// newInboundConnection returns a new InboundConnection object
func newInboundConnection(listener *Listener, logger *log.Logger, conn net.Conn) (*InboundConnection, error) {
	params := &InboundConnectionParameters{
		IdleTimeout:        time.Second * 30,
		ReadTimeout:        time.Second * 15,
		WriteTimeout:       time.Second * 15,
		GreetingHostname:   "localhost",
		GreetingMailserver: "goms",
		MaxMessageSize:     20 * 1024 * 1024,
	}
	c := &InboundConnection{
		plainConn: conn,
		listener:  listener,
		logger:    logger,
		params:    params,
		ITP:       &DummyITP{},
	}
	return c, nil
}

// Send sends a response to an inbound connection
func (c *InboundConnection) Send(r *ICResponse) error {
	c.conn.SetDeadline(time.Now().Add(c.params.WriteTimeout))

	c.logger.Printf("[DEBUG] Writing %v", r)

	for i, l := range r.lines {
		dashspace := " "
		if i != len(r.lines)-1 {
			dashspace = "-"
		}
		towrite := fmt.Sprintf("%03d%s%s\r\n", l.code, dashspace, l.text)

		for len(towrite) > 0 {
			if written, err := c.rdwr.WriteString(towrite); err != nil {
				return err
			} else {
				towrite = towrite[written:]
			}
		}
	}
	if r.canPipeline {
		c.needsFlush = true
	} else {
		c.needsFlush = false
		if err := c.rdwr.Flush(); err != nil {
			return err
		}
	}
	return nil
}

// Receive receives a command from an inbound connection
func (c *InboundConnection) Receive() (*ICCommand, error) {
	if c.needsFlush && c.rd.Buffered() == 0 {
		c.needsFlush = false
		if err := c.rdwr.Flush(); err != nil {
			return nil, err
		}
	}
	cmd := &ICCommand{}
	c.conn.SetDeadline(time.Now().Add(c.params.IdleTimeout))
	if line, isPrefix, err := c.rdwr.ReadLine(); err != nil {
		return nil, err
	} else if isPrefix {
		cmd.invalid = true
		// swallow the rest
		for {
			if _, isPrefix, err := c.rdwr.ReadLine(); err != nil {
				return nil, err
			} else if !isPrefix {
				break
			}
		}
		return cmd, nil
	} else {
		cmd.buf = line
		return cmd, nil
	}
}

// Process processes a command once received
func (c *InboundConnection) Process(ctx context.Context, cmd *ICCommand) (*ICResponse, error) {
	c.conn.SetDeadline(time.Now().Add(c.params.ReadTimeout))

	words := bytes.SplitN(bytes.Trim(cmd.buf, "\r\n"), []byte(" "), 2)

	if len(words) < 1 {
		// RFC5321 4.1.1
		return &ICResponse{lines: newICRL(500, "5.5.2 Error: bad syntax")}, nil
	} else if len(words) == 1 {
		words = [][]byte{words[0], []byte{}}
	}

	if v, ok := verbs[strings.ToUpper(string(words[0]))]; !ok {
		c.unrecognisedCommands++
		// RFC5321 4.2.4
		return &ICResponse{lines: newICRL(500, "5.5.2 Error: command unknown"), final: c.unrecognisedCommands > maxUnrecognisedCommands}, nil
	} else {
		return v.Run(c, ctx, words[1])
	}

	return &ICResponse{lines: newICRL(500, "5.5.0 Error: internal error")}, nil
}

// Serve processes an SMTP conversation, closing the connections etc. when done
func (c *InboundConnection) Serve(parentCtx context.Context) {
	c.conn = c.plainConn
	c.name = c.plainConn.RemoteAddr().String()
	if c.name == "" {
		c.name = "[unknown]"
	}

	c.logger.Printf("[INFO] Connection from %s", c.name)

	ctx, cancelFunc := context.WithCancel(parentCtx)
	defer func() {
		if c.tlsConn != nil {
			c.tlsConn.Close()
		}
		c.plainConn.Close()

		cancelFunc()
	}()

	// RFC5321 s4.5.3.1.4 - maximum size of a command line is 512 bytes (subject to extensisons)
	c.rd = bufio.NewReaderSize(c.conn, 4096)
	c.wr = bufio.NewWriter(c.conn)
	c.rdwr = bufio.NewReadWriter(c.rd, c.wr)

	done := make(chan struct{})
	go func() {
		if err := c.serveLoop(ctx); err != nil {
			c.logger.Printf("[DEBUG] Server loop return %v", err)
		}
		close(done)
	}()
	select {
	case <-ctx.Done():
		c.logger.Printf("[INFO] Parent forced close for %s", c.name)
	case <-done:
		c.logger.Printf("[INFO] Child quit for %s", c.name)
	}
}

// ServeLoop is an internal routine that processes an SMTP conversation
func (c *InboundConnection) serveLoop(ctx context.Context) error {

	// check with the ITP that this is acceptable
	if r, err := c.ITP.CheckConnection(ctx, c); err != nil {
		return err
	} else if r != nil && r.IsError() {
		return c.Send(r)
	}

	if err := c.Send(&ICResponse{
		lines: newICRL(220, fmt.Sprintf("%s ESMTP %s", c.params.GreetingHostname, c.params.GreetingMailserver)),
	}); err != nil {
		return err
	}

	c.logger.Println("[DEBUG] Starting server loop")

	for {
		if cmd, err := c.Receive(); err != nil {
			return err
		} else {
			if cmd.invalid {
				if err := c.Send(&ICResponse{
					// RFC5321 s4.5.3.1.4
					lines: newICRL(500, "5.5.0 Error: invalid line length"),
				}); err != nil {
					return err
				}
			} else if resp, err := c.Process(ctx, cmd); err != nil {
				return err
			} else {
				if err := c.Send(resp); err != nil {
					return err
				}
				if resp.final {
					break
				}
			}
		}
	}

	return nil
}
