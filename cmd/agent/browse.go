package main

// browseCommand opens the interactive desk browser. Until the TUI lands it
// falls back to the help text.
func browseCommand(e *env) error {
	e.tty = false
	return newRoot(e).Help()
}
