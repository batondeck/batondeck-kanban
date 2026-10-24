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
	inline := regexp.MustCompile(`(?s)<script>(.*?)</script>`).FindAllSubmatch(html, -1)
	if len(inline) == 0 {
		t.Fatal("no inline <script> in dist/index.html: drop its hash from AppCSP")
	}
	var want []string
	for _, m := range inline {
		sum := sha256.Sum256(m[1])
		want = append(want, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'")
	}
	script := regexp.MustCompile(`script-src ([^;]*)`).FindStringSubmatch(AppCSP)
	if got, w := script[1], "'self' "+strings.Join(want, " "); got != w {
		t.Errorf("script-src %q, want %q", got, w)
	}
}
