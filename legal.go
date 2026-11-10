// Package kanban carries the three texts at the repository's root that go with every copy of the
// binary: the licence, the NOTICE and the third-party notices. go:embed reaches only the directory
// of the package that embeds, so this package sits where the files are; `batondeck-kanban
// licenses` prints them (internal/cli).
package kanban

import _ "embed"

// License is LICENSE: the Apache License 2.0, which this repository is under.
//
//go:embed LICENSE
var License string

// Notice is NOTICE: the attribution an Apache-2.0 work carries.
//
//go:embed NOTICE
var Notice string

// ThirdPartyNotices is THIRD_PARTY_NOTICES: the licence texts of the Go modules the binary links
// and of the npm packages its embedded web UI is built from (`make notices` writes it).
//
//go:embed THIRD_PARTY_NOTICES
var ThirdPartyNotices string
