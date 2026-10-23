package oncall

// Untouched is autoclose's guard (conductor isUntouched, incident.ts:156-164, under ADR-0005): the
// incident ticket is TODO, unclaimed and never attempted, and nothing but the ingest has written it.
// "Nothing written" is ingestVersion == version: every ingest write stamps the version it leaves the
// ticket at, but only while the stamp still matches, so a write by anyone else (a comment, a context
// item, a field edit — each bumps the version) leaves the stamp behind for good, and a later ingest
// write cannot launder it. Only an untouched ticket is deleted on a resolve; any other is annotated.
func Untouched(status string, claimed bool, claimAttempts, version, ingestVersion int64) bool {
	return status == "TODO" && !claimed && claimAttempts == 0 && ingestVersion == version
}
