package smtpd

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
)

func TestTrueFalse(t *testing.T) {
	if tf, err := isTrue("true"); err != nil || tf != true {
		t.Fatalf("isTrue failed with 'true': %v %v", tf, err)
	}
	if tf, err := isTrue("false"); err != nil || tf != false {
		t.Fatalf("isTrue failed with 'false': %v %v", tf, err)
	}
	if tf, err := isTrue("neither"); err == nil || tf != false {
		t.Fatalf("isTrue failed with 'neither': %v %v", tf, err)
	}
	if tf, err := isFalse("false"); err != nil || tf != true {
		t.Fatalf("isFalse failed with 'false': %v %v", tf, err)
	}
	if tf, err := isFalse("true"); err != nil || tf != false {
		t.Fatalf("isFalse failed with 'true': %v %v", tf, err)
	}
	if tf, err := isFalse("neither"); err == nil || tf != false {
		t.Fatalf("isFalse failed with 'neither': %v %v", tf, err)
	}
	if tr, fa, err := isTrueFalse("true"); err != nil || tr != true || fa != false {
		t.Fatalf("isTrueFalse failed with 'true': %v %v %v", tr, fa, err)
	}
	if tr, fa, err := isTrueFalse("false"); err != nil || tr != false || fa != true {
		t.Fatalf("isTrueFalse failed with 'false': %v %v %v", tr, fa, err)
	}
	if tr, fa, err := isTrueFalse("neither"); err == nil || tr != false || fa != false {
		t.Fatalf("isTrueFalse failed with 'neither': %v %v %v", tr, fa, err)
	}
	if tr, fa, err := isTrueFalse(""); err != nil || tr != false || fa != false {
		t.Fatalf("isTrueFalse failed with '': %v %v %v", tr, fa, err)
	}
}

func writeConfig(t *testing.T, conf, fn string) {
	if err := ioutil.WriteFile(fn, []byte(conf), 0666); err != nil {
		t.Fatalf("Could not create config file: %v", err)
	}
}

func testConfig(t *testing.T, conf string, fn string, desc string, shouldWork bool) {
	writeConfig(t, conf, fn)
	c, err := ParseConfig(fn)
	if shouldWork {
		if c == nil || err != nil {
			t.Fatalf("Working config '%s' failed: %v, %v", desc, c, err)
		}
	} else {
		if c != nil || err == nil {
			t.Fatalf("Broken config '%s' passed: %v, %v", desc, c, err)
		}

	}
}

func TestConfigParser(t *testing.T) {
	dir, err := ioutil.TempDir("", "gomstest")
	if err != nil {
		t.Fatalf("Could not create temporary directory: %v", err)
	}
	defer os.RemoveAll(dir)
	fn := filepath.Join(dir, "goms.conf")

	if _, err := ParseConfig(fn + "-does-not-exist"); err == nil || !os.IsNotExist(err) {
		t.Fatalf("Non-existent config parsing failed: %v", err)
	}

	testConfig(t, `
zz
`,
		fn, "broken config", false)

	testConfig(t, `
servers:
- protocol: tcp
  address: 127.0.0.1:30025
- protocol: tcp
- address: 127.0.0.1:30025
logging:
  syslogfacility: local1
`,
		fn, "working config 1", true)

}
