package board

// Local is the server TollRoll runs on, read in process rather than through
// the fortress: rows of its own and lines for the shared log, in lang.
type Local interface {
	Rows(lang string) []App
	Lines(lang string) []Line
}
