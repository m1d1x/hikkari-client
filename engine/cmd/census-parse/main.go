// Command census-parse lists the entries of a subscription the operator paid
// for, as JSON without secrets. `ops census add` runs it, so the hub never needs
// a second link parser. The URL comes on stdin: an argument would show in ps.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"NekkoVPN/core/capacity"
	"NekkoVPN/core/engine/singbox"
	"NekkoVPN/core/subscription"
)

// Entry is what the census keeps of a profile. It is built field by field from
// the ones named here: a new secret in the config cannot reach it by accident.
type Entry struct {
	CapacityKey string `json:"capacityKey,omitempty"`
	Label       string `json:"label"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Protocol    string `json:"protocol"`
	Transport   string `json:"transport"`
	Security    string `json:"security"`
	SNI         string `json:"sni,omitempty"`
	// Config is the whole outbound, secrets included, only with --with-config:
	// ops hands it straight to the hub, which seals it for census proto.
	Config map[string]interface{} `json:"config,omitempty"`
}

func entryOf(d singbox.Draft) (Entry, error) {
	host, _ := d.Config["server"].(string)
	port := intField(d.Config["server_port"])
	if host == "" || port <= 0 {
		return Entry{}, errors.New("profile has no server address")
	}
	protocol, _ := d.Config["type"].(string)
	if protocol == "" {
		protocol = d.Kind
	}
	e := Entry{Label: d.Label, Host: host, Port: port, Protocol: protocol, Transport: "tcp", Security: "none"}
	if tr, ok := d.Config["transport"].(map[string]interface{}); ok {
		if t, _ := tr["type"].(string); t != "" {
			e.Transport = t
		}
	}
	if tls, ok := d.Config["tls"].(map[string]interface{}); ok {
		if on, _ := tls["enabled"].(bool); on {
			e.Security = "tls"
		}
		e.SNI, _ = tls["server_name"].(string)
		if r, ok := tls["reality"].(map[string]interface{}); ok {
			if on, _ := r["enabled"].(bool); on {
				e.Security = "reality"
			}
		}
	}
	return e, nil
}

func intField(v interface{}) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}

// run writes the entries of the subscription named on in. An error never
// carries the URL: it holds the subscription's token.
func run(ctx context.Context, in io.Reader, out io.Writer, withConfig bool) error {
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	subURL := strings.TrimSpace(line)
	if subURL == "" {
		return errors.New("no subscription URL on stdin")
	}
	imported, err := subscription.Fetch(ctx, subURL, "", func(link string) (struct {
		Draft singbox.Draft
		Key   string
	}, error) { d, err := singbox.ParseURI(link); return struct {
		Draft singbox.Draft
		Key   string
	}{d, capacity.Key(link)}, err })
	if err != nil {
		return errors.New(strings.ReplaceAll(err.Error(), subURL, "[subscription]"))
	}
	entries := make([]Entry, 0, len(imported.Items))
	for _, d := range imported.Items {
		e, err := entryOf(d.Draft)
		if err != nil {
			continue // AmneziaWG-style drafts without one address are not a TCP entry
		}
		if withConfig {
			e.Config = d.Draft.Config
			e.CapacityKey = d.Key
		}
		entries = append(entries, e)
	}
	return json.NewEncoder(out).Encode(entries)
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := run(ctx, os.Stdin, os.Stdout, len(os.Args) > 1 && os.Args[1] == "--with-config"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
