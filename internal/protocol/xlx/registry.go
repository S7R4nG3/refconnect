package xlx

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

// registryURL is the XLX central registry (rlx.lu) reflector list endpoint. It
// is served over plain HTTP — the HTTPS endpoint has been observed refusing
// connections, and this is a best-effort diagnostic, not a security boundary.
const registryURL = "http://xlxapi.rlx.lu/api.php?do=GetReflectorList"

// registryMaxBytes caps how much of the registry response we read (the full
// list is a few hundred KiB; this leaves generous headroom).
const registryMaxBytes = 16 << 20

// The registry emits technically-invalid XML: <comment> fields routinely carry
// unescaped '<', '>' and '&' characters, which a conformant parser
// (encoding/xml) rejects for the entire document. We only need three fields,
// all of which hold clean values — a reflector name, an IP, and a URL, none of
// which contain '<' — so we extract them with tolerant, field-scoped regexes
// instead of parsing the payload as XML.
var (
	reReflectorBlock = regexp.MustCompile(`(?s)<reflector>(.*?)</reflector>`)
	reName           = regexp.MustCompile(`<name>\s*([^<]+?)\s*</name>`)
	reLastIP         = regexp.MustCompile(`<lastip>\s*([^<]+?)\s*</lastip>`)
	reDashboard      = regexp.MustCompile(`<dashboardurl>\s*([^<]+?)\s*</dashboardurl>`)
)

// RegistryEntry is a single reflector record from the XLX central registry.
type RegistryEntry struct {
	Name      string
	LastIP    string
	Dashboard string
}

func firstSubmatch(re *regexp.Regexp, s string) string {
	m := re.FindStringSubmatch(s)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}

// LookupOrigin queries the XLX central registry for the given reflector (e.g.
// "XLX159") and returns its last-known origin IP and dashboard URL.
//
// It exists as a diagnostic: the registry records a reflector's real peering IP,
// which differs from a CDN/proxy-fronted DNS hostname. When a direct connection
// to the configured hostname times out, this reveals an address that is likely
// to actually accept the reflector's UDP traffic.
func LookupOrigin(ctx context.Context, reflectorName string) (RegistryEntry, error) {
	want := strings.ToUpper(strings.TrimSpace(reflectorName))
	if want == "" {
		return RegistryEntry{}, fmt.Errorf("xlx registry: empty reflector name")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, registryURL, nil)
	if err != nil {
		return RegistryEntry{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return RegistryEntry{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return RegistryEntry{}, fmt.Errorf("xlx registry: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, registryMaxBytes))
	if err != nil {
		return RegistryEntry{}, err
	}

	for _, block := range reReflectorBlock.FindAllStringSubmatch(string(body), -1) {
		inner := block[1]
		if !strings.EqualFold(firstSubmatch(reName, inner), want) {
			continue
		}
		ip := firstSubmatch(reLastIP, inner)
		if ip == "" {
			return RegistryEntry{}, fmt.Errorf("xlx registry: %s has no recorded IP", want)
		}
		return RegistryEntry{
			Name:      want,
			LastIP:    ip,
			Dashboard: firstSubmatch(reDashboard, inner),
		}, nil
	}
	return RegistryEntry{}, fmt.Errorf("xlx registry: %s not found", want)
}
