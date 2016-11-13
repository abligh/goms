package smtpd

import (
	"crypto/tls"
	"flag"
	"fmt"
	//	"github.com/sevlyar/go-daemon"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	_ "net/http/pprof"
)

/* Example configuration:

servers:
- protocol: tcp
  address: 127.0.0.1:25
- protocol: unix
  address: /var/run/goms.sock
logging:
  syslogfacility: local1
*/

// Location of the config file on disk; overriden by flags
var configFile = flag.String("c", "/etc/goms.conf", "Path to YAML config file")
var pidFile = flag.String("p", "/var/run/goms.pid", "Path to PID file")
var sendSignal = flag.String("s", "", "Send signal to daemon (either \"stop\" or \"reload\")")
var foreground = flag.Bool("f", false, "Run in foreground (not as daemon)")
var pprof = flag.Bool("pprof", false, "Run pprof")

const (
	ENV_CONFFILE = "_GOMS_CONFFILE"
	ENV_PIDFILE  = "_GOMS_PIDFILE"

	GOMS_DEFAULT_PORT = 25
)

// Map of configuration text to TLS versions
var tlsVersionMap = map[string]uint16{
	"ssl3.0": tls.VersionSSL30,
	"tls1.0": tls.VersionTLS10,
	"tls1.1": tls.VersionTLS11,
	"tls1.2": tls.VersionTLS12,
}

// Map of configuration text to TLS authentication strategies
var tlsClientAuthMap = map[string]tls.ClientAuthType{
	"none":          tls.NoClientCert,
	"request":       tls.RequestClientCert,
	"require":       tls.RequireAnyClientCert,
	"verify":        tls.VerifyClientCertIfGiven,
	"requireverify": tls.RequireAndVerifyClientCert,
}

// Config holds the config that applies to all servers (currently just logging), and an array of server configs
type Config struct {
	Servers []ServerConfig // array of server configs
	Logging LogConfig      // Configuration for logging
}

// ServerConfig holds the config that applies to each server (i.e. listener)
type ServerConfig struct {
	Protocol        string    // protocol it should listen on (in net.Conn form)
	Address         string    // address to listen on
	DefaultExport   string    // name of default export
	Tls             TlsConfig // TLS configuration
	DisableNoZeroes bool      // Disable NoZereos extension
}

// TlsConfig has the configuration for TLS
type TlsConfig struct {
	KeyFile    string // path to TLS key file
	CertFile   string // path to TLS cert file
	ServerName string // server name
	CaCertFile string // path to certificate file
	ClientAuth string // client authentication strategy
	MinVersion string // minimum TLS version
	MaxVersion string // maximum TLS version
}

// DriverConfig is an arbitrary map of other parameters in string format
type DriverParametersConfig map[string]string

// isTrue determines whether an argument is true
func isTrue(v string) (bool, error) {
	if v == "true" {
		return true, nil
	} else if v == "false" || v == "" {
		return false, nil
	}
	return false, fmt.Errorf("Unknown boolean value: %s", v)
}

// isFalse determines whether an argument is false
func isFalse(v string) (bool, error) {
	if v == "false" {
		return true, nil
	} else if v == "true" || v == "" {
		return false, nil
	}
	return false, fmt.Errorf("Unknown boolean value: %s", v)
}

// isTrue determines whether an argument is true or fals
func isTrueFalse(v string) (bool, bool, error) {
	if v == "true" {
		return true, false, nil
	} else if v == "false" {
		return false, true, nil
	} else if v == "" {
		return false, false, nil
	}
	return false, false, fmt.Errorf("Unknown boolean value: %s", v)
}

// ParseConfig parses the YAML configuration provided
func ParseConfig(confFile string) (*Config, error) {
	if buf, err := ioutil.ReadFile(confFile); err != nil {
		return nil, err
	} else {
		c := &Config{}
		if err := yaml.Unmarshal(buf, c); err != nil {
			return nil, err
		}
		for i, _ := range c.Servers {
			if c.Servers[i].Protocol == "" {
				c.Servers[i].Protocol = "tcp"
			}
			if c.Servers[i].Protocol == "tcp" && c.Servers[i].Address == "" {
				c.Servers[i].Protocol = fmt.Sprintf("0.0.0.0:%d", GOMS_DEFAULT_PORT)
			}
		}
		return c, nil
	}
}
