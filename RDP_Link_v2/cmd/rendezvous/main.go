package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/eminentailabs/office-rdp/rendezvous"
)

func main() {
	addr := flag.String("listen", ":8080", "listen address")
	flag.Parse()
	if err := rendezvous.Run(*addr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
