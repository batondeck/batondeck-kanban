package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// `licenses` prints the three root files as they are on disk, each under its name: a release is a
// bare binary, so the binary is where its recipient reads the licence it comes under.
func TestLicensesPrintsTheRootFiles(t *testing.T) {
	for _, cmd := range []string{"licenses", "licences"} {
		var out, errb bytes.Buffer
		if code := Run([]string{cmd}, "test", &out, &errb); code != 0 || errb.Len() != 0 {
			t.Fatalf("%s: exit %d, stderr %q", cmd, code, errb.String())
		}
		at := 0
		for _, name := range []string{"LICENSE", "NOTICE", "THIRD_PARTY_NOTICES"} {
			want, err := os.ReadFile("../../" + name)
			if err != nil {
				t.Fatal(err)
			}
			head := strings.Repeat("=", 80) + "\n" + name + "\n" + strings.Repeat("=", 80) + "\n\n"
			i := strings.Index(out.String()[at:], head+strings.TrimRight(string(want), "\n")+"\n")
			if i < 0 {
				t.Fatalf("%s: the output does not carry %s whole, after the files before it", cmd, name)
			}
			at += i + len(head) + len(want)
		}
	}
}

// It takes no argument, and answers -h on stdout like every other command.
func TestLicensesUsage(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run([]string{"licenses", "extra"}, "test", &out, &errb); code != 2 || out.Len() != 0 || !strings.Contains(errb.String(), `unexpected argument "extra"`) {
		t.Fatalf("an argument: exit %d, stdout %q, stderr %q", code, out.String(), errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"licenses", "-h"}, "test", &out, &errb); code != 0 || errb.Len() != 0 || !strings.Contains(out.String(), "usage: batondeck-kanban licenses") {
		t.Fatalf("-h: exit %d, stdout %q, stderr %q", code, out.String(), errb.String())
	}
}
