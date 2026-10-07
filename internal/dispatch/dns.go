package dispatch

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/KudcraftsHQ/purelymail-cli/internal/tree"
	"github.com/spf13/cobra"
)

// The records Purelymail needs on a custom domain, as documented at
// https://purelymail.com/docs/domainDocs (the Cloudflare walkthrough gives the
// exact names, targets and MX priority 50). The API itself only exposes the
// ownership value, through getOwnershipCode.
const (
	mxHost       = "mailserver.purelymail.com"
	mxPriority   = 50
	spfValue     = "v=spf1 include:_spf.purelymail.com ~all"
	dmarcTarget  = "dmarcroot.purelymail.com"
	ownershipKey = "purelymail_ownership_proof="
)

// DNSRecord is one record, shaped so it maps straight onto a DNS provider's API
// (Cloudflare's create-record body takes type, name, content, priority).
type DNSRecord struct {
	Type     string `json:"type"`
	Name     string `json:"name"`
	Content  string `json:"content"`
	Priority *int   `json:"priority,omitempty"`
	Purpose  string `json:"purpose"`
	Note     string `json:"note,omitempty"`
}

// DNSRecords builds the record set for a domain. ownership is the value from
// getOwnershipCode, or "" to leave that record out.
func DNSRecords(domain, ownership string) []DNSRecord {
	prio := mxPriority
	recs := []DNSRecord{
		{Type: "MX", Name: domain, Content: mxHost, Priority: &prio, Purpose: "mail delivery"},
		{Type: "TXT", Name: domain, Content: spfValue, Purpose: "SPF",
			Note: "merge into an existing SPF record rather than adding a second one"},
	}
	if ownership != "" {
		recs = append(recs, DNSRecord{Type: "TXT", Name: domain, Content: ownership,
			Purpose: "ownership proof (required by addDomain)"})
	}
	for i := 1; i <= 3; i++ {
		recs = append(recs, DNSRecord{
			Type:    "CNAME",
			Name:    fmt.Sprintf("purelymail%d._domainkey.%s", i, domain),
			Content: fmt.Sprintf("key%d.dkimroot.purelymail.com", i),
			Purpose: fmt.Sprintf("DKIM key %d", i),
			Note:    "DNS only (not proxied) on Cloudflare",
		})
	}
	recs = append(recs, DNSRecord{Type: "CNAME", Name: "_dmarc." + domain, Content: dmarcTarget,
		Purpose: "DMARC", Note: "DNS only (not proxied) on Cloudflare; replaces any existing _dmarc TXT"})
	return recs
}

// ownershipValue turns getOwnershipCode's result into the TXT record content.
// The spec calls it "the DNS record value", so it is used as-is when it already
// carries the purelymail_ownership_proof= prefix and given one otherwise.
func ownershipValue(code string) string {
	code = strings.TrimSpace(code)
	if code == "" || strings.HasPrefix(code, ownershipKey) {
		return code
	}
	return ownershipKey + code
}

func dnsCommand(rt *Runtime, res *tree.Resource, op *tree.Op) *cobra.Command {
	var skipOwnership bool

	cmd := &cobra.Command{
		Use:   "dns <domain>",
		Short: "Print the DNS records a domain needs for Purelymail",
		Long: op.Description + "\n\n" +
			"Values follow https://purelymail.com/docs/domainDocs. JSON by default, an array\n" +
			"of {type, name, content, priority?, purpose, note?} with fully qualified names;\n" +
			"-o table for reading.",
		Example: "  purelymail domain dns example.com\n" +
			"  purelymail domain dns example.com -o table\n" +
			"  purelymail domain dns example.com --skip-ownership   # offline, no token",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
	}
	cmd.Flags().BoolVar(&skipOwnership, "skip-ownership", false, "leave out the ownership TXT record and do not call the API")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		domain := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(args[0])), ".")
		if domain == "" || strings.ContainsAny(domain, " /@") || !strings.Contains(domain, ".") {
			return fmt.Errorf("%q does not look like a domain name", args[0])
		}

		ownership := ""
		if !skipOwnership {
			raw, err := rt.Client.Do(cmd.Context(), op.Operation, nil)
			if err != nil {
				return fmt.Errorf("fetch ownership code (use --skip-ownership to omit it): %w", err)
			}
			if rt.DryRun {
				ownership = "(dry run: value comes from getOwnershipCode)"
			} else {
				var result struct {
					Code string `json:"code"`
				}
				if err := json.Unmarshal(raw, &result); err != nil || result.Code == "" {
					return fmt.Errorf("getOwnershipCode returned no code: %s", string(raw))
				}
				ownership = ownershipValue(result.Code)
			}
		}
		if len(rt.Columns) == 0 {
			rt.Columns = []string{"type", "name", "content", "priority", "purpose", "note"}
		}
		return rt.RenderValue(toAny(DNSRecords(domain, ownership)))
	}
	return cmd
}

// toAny round-trips through JSON so the output package sees plain maps, which
// is what lets -o table and csv lay the records out as rows.
func toAny(v any) any {
	raw, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(raw, &out)
	return out
}

func rawValue(raw []byte) any {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	return v
}
