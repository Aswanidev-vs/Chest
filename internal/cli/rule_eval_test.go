package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runRuleEval executes `chest rule eval` the way a user would type it and
// returns everything it printed.
func runRuleEval(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	cmd := newRuleCmd()
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(append([]string{"eval"}, args...))
	err := cmd.Execute()
	return buf.String(), err
}

func writeEvalFixture(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

// TestRuleEvalExplainsTheMatchingRule is the whole point of the command: for a
// file that a rule matches, it names the rule by index and name, shows the
// destination with placeholders expanded, and prints the same WHY line
// `sort --dry-run` prints.
func TestRuleEvalExplainsTheMatchingRule(t *testing.T) {
	usePlanTestHome(t)

	dir := t.TempDir()
	file := writeEvalFixture(t, dir, "notes.txt", "hello notes")

	out, err := runRuleEval(t, file, "--rule", "*.txt -> Docs/Notes")
	require.NoError(t, err)

	assert.Contains(t, out, "RULE SET", "the resolved rule set must be printed")
	assert.Contains(t, out, "[0]", "rules must be numbered in evaluation order")
	assert.Contains(t, out, "*.txt -> Docs/Notes", "the rule that matched must be named")
	assert.Contains(t, out, "MATCH")
	assert.Contains(t, out, "rule #0")
	assert.Contains(t, out, "WHY")

	// The destination is shown relative to the file's own directory, with the
	// route's folder structure and the filename in place.
	assert.Contains(t, out, filepath.Join("Docs", "Notes", "notes.txt"))
	assert.NotContains(t, out, filepath.Join(dir, "Docs", "Notes", "notes.txt")+"\x00")
}

// TestRuleEvalExpandsPlaceholders covers the destination placeholders the
// planner resolves.
func TestRuleEvalExpandsPlaceholders(t *testing.T) {
	usePlanTestHome(t)

	dir := t.TempDir()
	file := writeEvalFixture(t, dir, "clip.mp4", "not really a video")

	out, err := runRuleEval(t, file, "--rule", "ext=mp4 -> Videos/{ext}")
	require.NoError(t, err)

	assert.Contains(t, out, filepath.Join("Videos", "MP4", "clip.mp4"),
		"{ext} must be expanded to the upper-cased extension")
}

// TestRuleEvalReportsNoMatchAsSuccess: an unmatched file is an answer, not an
// error, and says so plainly.
func TestRuleEvalReportsNoMatchAsSuccess(t *testing.T) {
	usePlanTestHome(t)

	dir := t.TempDir()
	file := writeEvalFixture(t, dir, "mystery.qqq", "no rule wants me")

	out, err := runRuleEval(t, file, "--rule", "*.txt -> Docs")
	require.NoError(t, err, "a file that matches nothing is not a failure")

	assert.Contains(t, strings.ToUpper(out), "NO RULE MATCHED")
	assert.NotContains(t, out, filepath.Join("Docs", "mystery.qqq"))
}

// TestRuleEvalShowsTheFullResolvedRuleSet keeps `rule eval` honest about what
// it compared against: with a preset the user can see every candidate rule.
func TestRuleEvalShowsTheFullResolvedRuleSet(t *testing.T) {
	usePlanTestHome(t)

	dir := t.TempDir()
	file := writeEvalFixture(t, dir, "photo.jpg", "\xFF\xD8\xFF\xE0 not really a jpeg")

	out, err := runRuleEval(t, file, "--type")
	require.NoError(t, err)

	assert.Contains(t, out, "Type: Image")
	assert.Contains(t, out, "Images")
	assert.Contains(t, out, "MATCH")
}

// TestRuleEvalIsReadOnly proves the evaluation itself changes nothing.
func TestRuleEvalIsReadOnly(t *testing.T) {
	usePlanTestHome(t)

	dir := t.TempDir()
	file := writeEvalFixture(t, dir, "notes.txt", "hello notes")

	before := snapshotPlanTree(t, dir)
	_, err := runRuleEval(t, file, "--type", "--rule", "*.txt -> Docs")
	require.NoError(t, err)
	assert.Equal(t, before, snapshotPlanTree(t, dir))
	assert.FileExists(t, file)
	assert.NoDirExists(t, filepath.Join(dir, "Docs"))
}

func TestRuleEvalRejectsBadInput(t *testing.T) {
	usePlanTestHome(t)

	dir := t.TempDir()
	writeEvalFixture(t, dir, "notes.txt", "hello notes")

	_, err := runRuleEval(t, filepath.Join(dir, "missing.txt"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot read")

	_, err = runRuleEval(t, dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is a directory")
}

// TestRuleCheckRegression guards the behavior `rule check` had before
// `rule eval` shared its flag binding.
func TestRuleCheckRegression(t *testing.T) {
	usePlanTestHome(t)

	dir := t.TempDir()

	var buf bytes.Buffer
	check := newRuleCmd()
	check.SetOut(&buf)
	check.SetErr(&buf)
	check.SetArgs([]string{"check", dir, "--rule", "ext=jpg -> Images"})
	require.NoError(t, check.Execute())

	buf.Reset()
	broken := newRuleCmd()
	broken.SetOut(&buf)
	broken.SetErr(&buf)
	broken.SetArgs([]string{"check", dir, "--rule", "size>abc -> Huge"})
	err := broken.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid rule(s)")
}

// TestRuleSubcommandsAreRegistered keeps both utilities reachable from
// `chest rule`.
func TestRuleSubcommandsAreRegistered(t *testing.T) {
	names := map[string]bool{}
	for _, c := range newRuleCmd().Commands() {
		names[c.Name()] = true
	}
	assert.True(t, names["check"], "chest rule check must still be registered")
	assert.True(t, names["eval"], "chest rule eval must be registered")
}

// TestRuleFlagsMatchAcrossSubcommands: `rule eval` accepts exactly what
// `rule check` accepts, flag for flag and shorthand for shorthand.
func TestRuleFlagsMatchAcrossSubcommands(t *testing.T) {
	collect := func(cmd *cobra.Command) map[string]string {
		flags := map[string]string{}
		cmd.Flags().VisitAll(func(f *pflag.Flag) { flags[f.Name] = f.Shorthand })
		return flags
	}

	checkFlags := collect(newRuleCheckCmd())
	evalFlags := collect(newRuleEvalCmd())

	assert.Equal(t, checkFlags, evalFlags)
	assert.Equal(t, map[string]string{
		"type":   "t",
		"format": "f",
		"size":   "s",
		"date":   "d",
		"preset": "p",
		"rule":   "r",
	}, checkFlags)
}
