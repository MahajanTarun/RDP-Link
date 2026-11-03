package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/eminentailabs/office-rdp/rendezvous"
)

func main() {
	addr := flag.String("listen", ":8080", "listen address")
	cert := flag.String("tls-cert", "", "TLS certificate file (enables HTTPS/WSS)")
	key := flag.String("tls-key", "", "TLS private key file")
	publicURL := flag.String("public-url", "", "public base URL used in relay links (e.g. https://relay.example.com)")
	adminToken := flag.String("admin-token", "",
		"token that unlocks the full peer listing (also read from RDPLINK_ADMIN_TOKEN)")
	flag.Parse()
	token := *adminToken
	if token == "" {
		token = strings.TrimSpace(os.Getenv("RDPLINK_ADMIN_TOKEN"))
	}
	if err := rendezvous.RunOptionsWithAdmin(*addr, *cert, *key, *publicURL, token); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
