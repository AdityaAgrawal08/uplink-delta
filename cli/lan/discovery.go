package lan

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/mdns"
)

type ServiceInfo struct {
	Hostname    string
	Port        int
	ShareCode   string
	FileName    string
	Size        int64
	Fingerprint string
}

func RegisterService(info ServiceInfo) (func(), error) {
	service, err := mdns.NewMDNSService(
		info.Hostname,
		"_uplink._tcp",
		"", // domain - empty = local
		"", // host - empty = auto-detect
		info.Port,
		nil, // IPs - empty = auto-detect
		[]string{info.ShareCode, info.FileName, strconv.FormatInt(info.Size, 10), info.Fingerprint},
	)
	if err != nil {
		return nil, fmt.Errorf("mDNS service create: %w", err)
	}
	server, err := mdns.NewServer(&mdns.Config{Zone: service})
	if err != nil {
		return nil, fmt.Errorf("mDNS server start: %w", err)
	}
	return func() { _ = server.Shutdown() }, nil
}

func DiscoverService(ctx context.Context, shareCode string) (string, string, int64, string, error) {
	entriesCh := make(chan *mdns.ServiceEntry, 20)

	qp := &mdns.QueryParam{
		Service: "_uplink._tcp",
		Domain:  "local",
		Timeout: 3 * time.Second,
		Entries: entriesCh,
	}

	go func() {
		defer close(entriesCh)
		_ = mdns.Query(qp)
	}()

	for {
		select {
		case entry, ok := <-entriesCh:
			if !ok {
				return "", "", 0, "", fmt.Errorf("peer not found on LAN (mDNS search completed)")
			}
			if len(entry.InfoFields) >= 4 && entry.InfoFields[0] == shareCode {
				host := entry.AddrV4.String()
				if entry.AddrV4 == nil {
					host = entry.Host
				}
				host = strings.TrimSuffix(host, ".")
				addr := net.JoinHostPort(host, strconv.Itoa(entry.Port))
				filename := entry.InfoFields[1]
				size, _ := strconv.ParseInt(entry.InfoFields[2], 10, 64)
				fingerprint := entry.InfoFields[3]
				return addr, filename, size, fingerprint, nil
			}
		case <-ctx.Done():
			return "", "", 0, "", ctx.Err()
		}
	}
}
