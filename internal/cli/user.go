package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// stdin is where a password is read from; tests replace it with a pipe.
var stdin = os.Stdin

// readPassword reads the first line of stdin. A terminal is refused rather than read, because it
// would echo the password: pipe it in instead.
func readPassword() (string, error) {
	if fi, err := stdin.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		return "", errors.New("the password is read from stdin, which is a terminal and would echo it: pipe it in, e.g. `read -rs PW; printf '%s\\n' \"$PW\" | batondeck-kanban user ...`")
	}
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// authStore opens the configured store for the user and token commands: it must exist and be
// migrated (`batondeck-kanban migrate`), since these commands never create or migrate one.
func authStore(ctx context.Context, cfgPath string) (store.Store, error) {
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		return nil, err
	}
	st, err := openStore(ctx, cfg, false)
	if err != nil {
		return nil, fmt.Errorf("%s: %w (run `batondeck-kanban migrate` first)", cfg.Engine(), err)
	}
	if err := st.SchemaCurrent(ctx); err != nil {
		st.Close()
		return nil, err
	}
	return st, nil
}

func userUsage(w io.Writer) {
	fmt.Fprint(w, `usage: batondeck-kanban user <subcommand> [flags]

  add [--admin] [--name NAME] LOGIN   create a user; the password is read from stdin
  disable LOGIN                       disable a user: their sessions and tokens stop within 60 s
  passwd LOGIN                        set a user's password, read from stdin
  list                                list the users

every subcommand takes -config PATH.
`)
}

// user is `batondeck-kanban user add|disable|passwd|list`.
func user(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		userUsage(stderr)
		return 2
	}
	sub, rest := args[0], args[1:]
	var cfgPath, name string
	fs := commonFlags("user "+sub, &cfgPath, stderr)
	switch sub {
	case "add":
		fs.StringVar(&name, "name", "", "display name (default: the login before any @)")
	case "disable", "passwd", "list":
	default:
		fmt.Fprintf(stderr, "user: unknown subcommand %q\n", sub)
		userUsage(stderr)
		return 2
	}
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	wantArgs := 1
	if sub == "list" {
		wantArgs = 0
	}
	if fs.NArg() != wantArgs {
		userUsage(stderr)
		return 2
	}
	ctx := context.Background()
	st, err := authStore(ctx, cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "user %s: %v\n", sub, err)
		return 1
	}
	defer st.Close()
	acc := &auth.Accounts{Store: st}
	fail := func(err error) int {
		fmt.Fprintf(stderr, "user %s: %v\n", sub, err)
		return 1
	}
	switch sub {
	case "add":
		pw, err := readPassword()
		if err != nil {
			return fail(err)
		}
		u, err := acc.Add(ctx, fs.Arg(0), name, pw)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "user add: %s %s\n", u.ID, u.Email)
	case "disable":
		u, err := acc.Disable(ctx, fs.Arg(0))
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "user disable: %s %s disabled at %s\n", u.ID, u.Email, time.UnixMilli(u.DisabledAt).UTC().Format(time.RFC3339))
	case "passwd":
		pw, err := readPassword()
		if err != nil {
			return fail(err)
		}
		if err := acc.SetPassword(ctx, fs.Arg(0), pw); err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "user passwd: password set for %s\n", strings.ToLower(strings.TrimSpace(fs.Arg(0))))
	case "list":
		us, err := acc.List(ctx)
		if err != nil {
			return fail(err)
		}
		tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tLOGIN\tNAME\tCREATED\tDISABLED")
		for _, u := range us {
			disabled := "-"
			if u.DisabledAt != 0 {
				disabled = time.UnixMilli(u.DisabledAt).UTC().Format(time.RFC3339)
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", u.ID, u.Email, u.DisplayName, time.UnixMilli(u.CreatedAt).UTC().Format(time.RFC3339), disabled)
		}
		tw.Flush()
	}
	return 0
}
