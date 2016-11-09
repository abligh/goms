package main

import (
	"flag"
	"github.com/abligh/goms/goms"
)

// main() is the main program entry
//
// this is a wrapper to enable us to put the interesting stuff in a package
func main() {
	flag.Parse()
	goms.Run(nil)
}
