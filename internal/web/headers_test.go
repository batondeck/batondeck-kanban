package web

import (
	"crypto/sha256"
	"encoding/base64"
	"regexp"
	"strings"
	"testing"

	webdist "github.com/batondeck/batondeck-kanban/web"
)

// TestAppCSPHashesTheInlineScripts recomputes the hash of every inline <script> in the embedded
// index.html: a rebuilt bootstrap that AppCSP does not name would be refused in the browser.
func TestAppCSPHashesTheInlineScripts(t *testing.T) {
	html, err := webdist.Dist.ReadFile("dist/index.html")
	if err != nil {
		t.Fatal(err)
	}
	// Every <script> whatever its attributes; one with a src is not inline and needs no hash.
	var want []string
	for _, m := range regexp.MustCompile(`(?is)<script\b([^>]*)>(.*?)</script>`).FindAllSubmatch(html, -1) {
		if regexp.MustCompile(`(?i)\bsrc\s*=`).Match(m[1]) {
			continue
		}
		sum := sha256.Sum256(m[2])
		want = append(want, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'")
	}
	if len(want) == 0 {
		t.Fatal("no inline <script> in dist/index.html: drop its hash from AppCSP")
	}
	script := regexp.MustCompile(`script-src ([^;]*)`).FindStringSubmatch(AppCSP)
	if got, w := script[1], "'self' "+strings.Join(want, " "); got != w {
		t.Errorf("script-src %q, want %q", got, w)
	}
}

// TestAppCSPNamesNoThirdPartyHost: the SPA bundles its fonts and its link cards ask no favicon
// service (web/src/settings.ts linkFavicons is off), so every source is this origin, a hash, a
// keyword or data:. A host here would let a page tell a third party what a viewer is looking at.
func TestAppCSPNamesNoThirdPartyHost(t *testing.T) {
	for _, directive := range strings.Split(AppCSP, ";") {
		fields := strings.Fields(directive)
		if len(fields) == 0 {
			continue
		}
		for _, src := range fields[1:] {
			if strings.HasPrefix(src, "'") || src == "data:" {
				continue
			}
			t.Errorf("%s names %q, a host other than this origin", fields[0], src)
		}
	}
}
