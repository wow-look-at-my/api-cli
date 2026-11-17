// The <run> of a <join>: a program that makes the output from the joined parts.

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
)

// planJoinRun renders a join's program against the part's record, plus
// .join.input and .join.output. It renders at plan time. A bad template fails
// before the first byte. .join.output is the .part sibling of the output. The
// output gets its real name only after the program exits 0.
func planJoinRun(run *Cmd, part *joinPart, ctx map[string]any, to string) error {
	if run == nil {
		return nil
	}
	part.Input = joinInputPath(ctx, part.Dest, to)
	rctx := make(map[string]any, len(ctx)+1)
	for k, v := range ctx {
		rctx[k] = v
	}
	rctx["join"] = map[string]any{"input": part.Input, "output": part.Dest + ".part"}
	argv, err := resolveArgv(run, rctx)
	if err != nil {
		return err
	}
	part.Argv = argv
	return nil
}

// joinInputPath names the file the parts join into. It goes in the run's
// scratch directory when there is one, which removes it when the run ends.
// The ".joined" suffix keeps the output's extension off a file that is not
// in that format yet.
func joinInputPath(ctx map[string]any, dest, to string) string {
	if run, ok := ctx["run"].(map[string]any); ok {
		if tmp, ok := run["tmpdir"].(string); ok && tmp != "" {
			return filepath.Join(tmp, "join", filepath.Clean("/"+to)) + ".joined"
		}
	}
	return dest + ".joined"
}

// checkRun rejects a group whose members rendered different programs. One
// output has one program, and picking one of them silently drops the others.
func (j *joiner) checkRun(g *joinGroup) error {
	for _, item := range g.members {
		if !slices.Equal(item.spec.Join.Argv, g.spec.Argv) {
			return fmt.Errorf("group %q: <join><run> rendered both `%s` and `%s`; it can read only the values every part of the group shares",
				g.spec.Group, shownArgv(g.spec.Argv), shownArgv(item.spec.Join.Argv))
		}
	}
	return nil
}

// runProgram runs a group's <join><run>. The program's stdout and stderr go to
// errOut, because stdout of this process lists the files that landed. A
// failure removes what the program wrote and keeps the parts.
func (j *joiner) runProgram(g *joinGroup, part string) error {
	argv := g.spec.Argv
	j.log("running %s for %s", filepath.Base(argv[0]), filepath.Base(g.dest))

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout = j.errOut
	cmd.Stderr = j.errOut
	logVerbose("join: exec %s", shownArgv(argv))
	err := cmd.Run()
	if err == nil {
		if _, serr := os.Stat(part); serr != nil {
			err = errors.New("exited 0 but wrote no file at .join.output")
		}
	}
	if err == nil {
		return nil
	}
	os.Remove(part)
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		err = fmt.Errorf("exited with %s", exit)
	}
	return fmt.Errorf("%s: group %q: <join><run> `%s` %v; the parts are kept",
		g.dest, g.spec.Group, shownArgv(argv), err)
}

// shownArgv shows an argv the way a shell reads it back.
func shownArgv(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	return commandLine(argv[0], argv[1:])
}
