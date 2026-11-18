package board

// Local is the server TollRoll runs on, read in process rather than through
// the fortress: rows of its own and lines for the shared log, in lang.
type Local interface {
	Rows(lang string) []App
	Lines(lang string) []Line
}

// Locals are several Locals on one board, their rows and lines in order.
type Locals []Local

// Rows returns the rows of each.
func (ls Locals) Rows(lang string) []App {
	var out []App
	for _, l := range ls {
		out = append(out, l.Rows(lang)...)
	}
	return out
}

// Lines returns the lines of each.
func (ls Locals) Lines(lang string) []Line {
	var out []Line
	for _, l := range ls {
		out = append(out, l.Lines(lang)...)
	}
	return out
}
