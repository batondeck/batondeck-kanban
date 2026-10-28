package cli

import (
	"context"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/domain"
)

func tokenUsage(w io.Writer) {
	fmt.Fprint(w, `usage: batondeck-kanban token <subcommand> [flags]

  create --user LOGIN [--label L] [--ttl D]   mint a token for a user; prints it ONCE
  list [--user LOGIN]                         list tokens: prefix, user, label, times, status
  revoke ID                                   revoke a token (its bd_<id> prefix or bare id)

--label names the agent that works on the token: its tool calls, claims and events record it. A
label is one user's: create refuses a label another user's live token carries. Below master, naming
a label as claim_next's or next_task's assignee, or as handoff_task's toAgent, is refused unless the
caller is on a token carrying it (claim_task by task id does not check the assignee). It defaults to
"cli", which names no agent (each MCP client is then told apart by its own name). --ttl is a Go duration (720h is 30 days, the default;
at most 8760h).
every subcommand takes -config PATH.
`)
}

// token is `batondeck-kanban token create|list|revoke`.
func token(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		tokenUsage(stderr)
		return 2
	}
	sub, rest := args[0], args[1:]
	var cfgPath, login, label string
	var ttl time.Duration
	fs := commonFlags("token "+sub, &cfgPath, stderr)
	wantArgs := 0
	switch sub {
	case "create":
		fs.StringVar(&login, "user", "", "the user the token acts for (required)")
		fs.StringVar(&label, "label", domain.DefaultTokenLabel, "the agent label the token works as")
		fs.DurationVar(&ttl, "ttl", auth.DefaultTokenTTL, "how long the token lives")
	case "list":
		fs.StringVar(&login, "user", "", "only this user's tokens")
	case "revoke":
		wantArgs = 1
	default:
		fmt.Fprintf(stderr, "token: unknown subcommand %q\n", sub)
		tokenUsage(stderr)
		return 2
	}
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	if fs.NArg() != wantArgs || (sub == "create" && login == "") {
		tokenUsage(stderr)
		return 2
	}
	fail := func(err error) int {
		fmt.Fprintf(stderr, "token %s: %v\n", sub, err)
		return 1
	}
	ctx := context.Background()
	st, err := authStore(ctx, cfgPath)
	if err != nil {
		return fail(err)
	}
	defer st.Close()
	acc := &auth.Accounts{Store: st}
	ts := &auth.TokenService{Store: st, Audit: st}
	switch sub {
	case "create":
		u, err := acc.Lookup(ctx, login)
		if err != nil {
			return fail(err)
		}
		if u.DisabledAt != 0 {
			return fail(fmt.Errorf("%s is disabled", u.Email))
		}
		plain, info, err := ts.Mint(ctx, auth.HostActor, "cli", u.ID, u.Email, label, ttl)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintln(stdout, plain)
		fmt.Fprintf(stderr, "token create: %s for %s, label %q, expires %s. This is the only time the token is shown.\n",
			info.Display(), u.Email, info.Label, info.ExpiresAt.UTC().Format(time.RFC3339))
	case "list":
		identity := ""
		if login != "" {
			u, err := acc.Lookup(ctx, login)
			if err != nil {
				return fail(err)
			}
			identity = u.ID
		}
		infos, err := ts.List(ctx, identity)
		if err != nil {
			return fail(err)
		}
		users, err := acc.List(ctx)
		if err != nil {
			return fail(err)
		}
		logins := map[string]string{}
		for _, u := range users {
			logins[u.ID] = u.Email
		}
		tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "TOKEN\tUSER\tLABEL\tCREATED\tEXPIRES\tSTATUS")
		for _, i := range infos {
			who := logins[i.IdentityID]
			if who == "" {
				who = i.IdentityID
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", i.Display(), who, i.Label,
				i.CreatedAt.UTC().Format(time.RFC3339), i.ExpiresAt.UTC().Format(time.RFC3339), i.Status)
		}
		tw.Flush()
	case "revoke":
		info, err := ts.RevokeOwned(ctx, auth.HostActor, "cli", "", fs.Arg(0))
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "token revoke: %s revoked at %s\n", info.Display(), info.RevokedAt.UTC().Format(time.RFC3339))
	}
	return 0
}
