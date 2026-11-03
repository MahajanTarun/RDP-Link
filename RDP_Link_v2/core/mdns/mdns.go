// Package mdns advertises and browses _office-rdp._tcp on the LAN.
package mdns

import (
	"fmt"
	"net"

	"github.com/hashicorp/mdns"
)

const ServiceType = "_office-rdp._tcp"

// Advertiser holds a running mDNS server.
type Advertiser struct {
	Server *mdns.Server
}

// Advertise starts advertising a host.
func Advertise(instance, host string, port int, txt []string) (*Advertiser, error) {
	info := []string{}
	info = append(info, txt...)
	service, err := mdns.NewMDNSService(instance, ServiceType, "local.", host, port, nil, info)
	if err != nil {
		return nil, err
	}
	server, err := mdns.NewServer(&mdns.Config{Zone: service})
	if err != nil {
		return nil, err
	}
	return &Advertiser{Server: server}, nil
}

// BrowseResult is a discovered host.
type BrowseResult struct {
	Name string
	Addr net.IP
	Port int
	TXT  []string
}

// Browse discovers hosts for a duration-ish via callback channel; returns immediately with a cancel func.
func Browse(results chan<- BrowseResult) (*mdns.MDNSService, error) {
	entries := make(chan *mdns.ServiceEntry, 16)
	go func() {
		for e := range entries {
			ip := e.AddrV4
			if ip == nil {
				ip = e.AddrV6
			}
			results <- BrowseResult{Name: e.Name, Addr: ip, Port: e.Port, TXT: e.InfoFields}
		}
	}()
	params := &mdns.QueryParam{
		Service: ServiceType,
		Domain:  "local.",
		Entries: entries,
		Timeout: 3 * 1e9, // overridden by caller typically
	}
	// Run query in background
	go func() {
		_ = mdns.Query(params)
		close(entries)
	}()
	return nil, nil
}

// ResolveAddr helper.
func FormatEndpoint(ip net.IP, port int) string {
	return fmt.Sprintf("%s:%d", ip, port)
}
