package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Every command that deletes something asks the same question in the same way,
// so the question lives here once. Each caller prints what it is about to
// delete first, then asks.

// yesOption reports whether one command line option is the confirmation that
// skips the question.
func yesOption(option string) bool {
	return option == "--yes" || option == "-y"
}

// confirmYes asks for the word yes and reports whether it was given.
//
// Only "yes" agrees. A shorter answer such as "y" does not, because the word
// is what makes the choice deliberate. End of input is a no, not a failure: a
// command with nothing on its input has not been agreed to.
func confirmYes(in io.Reader, out io.Writer) (bool, error) {
	fmt.Fprintf(out, "Type yes to continue: ")

	answer, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("read the answer: %w", err)
	}
	return strings.TrimSpace(strings.ToLower(answer)) == "yes", nil
}
