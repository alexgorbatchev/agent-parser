package parser

import (
	"path"
	"regexp"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// Skill invocation sources.
const (
	// SkillSourceModel marks a skill the model loaded on its own.
	SkillSourceModel = "model"
	// SkillSourceUser marks a skill the user invoked explicitly, such as a slash command.
	SkillSourceUser = "user"
)

// SkillInvocation records one use of an agent skill.
type SkillInvocation struct {
	// Name is the skill's name as the harness reports it, such as "docs-writer".
	Name string `json:"name"`
	// Source is SkillSourceModel or SkillSourceUser.
	Source string `json:"source"`
	// Path is the skill's SKILL.md file or directory when the harness records it.
	Path string `json:"path,omitempty"`
}

const skillFileName = "SKILL.md"

// skillFromFile returns the skill whose definition file is filePath. It recognizes the
// installed-skill layouts harnesses use: skills/<name>/SKILL.md, Codex system skills at
// skills/.system/<name>/SKILL.md, and synced skills at skills/synced/<id>/<name>/SKILL.md.
// Other nesting, such as registry sources at skills/user/<name>/SKILL.md, does not qualify.
func skillFromFile(filePath, source string) (SkillInvocation, bool) {
	clean := path.Clean(strings.ReplaceAll(filePath, "\\", "/"))
	if path.Base(clean) != skillFileName {
		return SkillInvocation{}, false
	}
	dir := path.Dir(clean)
	name := path.Base(dir)
	parent := path.Dir(dir)
	grandparent := path.Dir(parent)
	installed := path.Base(parent) == "skills" ||
		(path.Base(parent) == ".system" && path.Base(grandparent) == "skills") ||
		(path.Base(grandparent) == "synced" && path.Base(path.Dir(grandparent)) == "skills")
	if !installed || name == "." || name == "/" {
		return SkillInvocation{}, false
	}
	return SkillInvocation{Name: name, Source: source, Path: clean}, true
}

// shellReadCommands are commands that read the files they are given from the beginning.
// tail is absent because it reads from the end, so it never starts a skill load.
var shellReadCommands = map[string]bool{
	"cat": true, "sed": true, "head": true, "nl": true, "bat": true, "less": true, "more": true,
}

var sedLineRange = regexp.MustCompile(`^(\d+)(,\d+)?p$`)

var shellCommandSeparator = regexp.MustCompile(`&&|\|\||[;\n]`)

// skillsFromShellCommand returns the skills whose SKILL.md a shell script reads from the
// beginning. Only pipelines whose first stage reads files and redirects nothing count, so
// edits, listings, and commands that merely mention a SKILL.md path are ignored. A pipeline
// with a stage that skips the start of its input, such as `nl FILE | sed -n '120,300p'`,
// continues an earlier load and is ignored too. Each skill is reported once.
func skillsFromShellCommand(script string) []SkillInvocation {
	var skills []SkillInvocation
	seen := map[string]bool{}
	for _, command := range shellCommandSeparator.Split(script, -1) {
		stages := strings.Split(command, "|")
		fields := shellSkillFields(stages[0])
		if len(fields) == 0 || !shellReadCommands[path.Base(trimShellQuotes(fields[0]))] {
			continue
		}
		if strings.ContainsAny(command, "<>") || editsInPlace(fields) || pipelineContinuesRead(stages) {
			continue
		}
		for _, field := range fields[1:] {
			skill, ok := skillFromFile(trimShellQuotes(field), SkillSourceModel)
			if ok && !seen[skill.Name] {
				seen[skill.Name] = true
				skills = append(skills, skill)
			}
		}
	}
	return skills
}

// pipelineContinuesRead reports whether any stage of a pipeline drops the start of the
// file: a sed range starting past line 1, or tail.
func pipelineContinuesRead(stages []string) bool {
	for _, stage := range stages {
		fields := strings.Fields(stage)
		if len(fields) == 0 {
			continue
		}
		if continuesRead(fields) || path.Base(trimShellQuotes(fields[0])) == "tail" {
			return true
		}
	}
	return false
}

// editsInPlace reports whether a sed command rewrites its files (-i, -i.bak, --in-place).
func editsInPlace(fields []string) bool {
	if path.Base(trimShellQuotes(fields[0])) != "sed" {
		return false
	}
	for _, field := range fields[1:] {
		if strings.HasPrefix(field, "-i") || strings.HasPrefix(field, "--in-place") {
			return true
		}
	}
	return false
}

// continuesRead reports whether a sed command prints a line range that starts after
// line 1, which continues paging through a file whose first chunk was already counted.
func continuesRead(fields []string) bool {
	if path.Base(trimShellQuotes(fields[0])) != "sed" {
		return false
	}
	for _, field := range fields[1:] {
		if match := sedLineRange.FindStringSubmatch(trimShellQuotes(field)); match != nil {
			return match[1] != "1"
		}
	}
	return false
}

func trimShellQuotes(token string) string {
	return strings.Trim(token, `"'`)
}

// stringInput returns input[key] when it is a non-blank string.
func stringInput(input map[string]interface{}, key string) string {
	if value, ok := input[key].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

// skillsFromToolInput returns the skill a dedicated skill-loading tool call names in
// input[inputKey]. Claude Code's Skill tool uses "skill"; OpenCode's skill tool uses "name".
func skillsFromToolInput(inputKey string, input map[string]interface{}) []SkillInvocation {
	name := stringInput(input, inputKey)
	if name == "" {
		return nil
	}
	return []SkillInvocation{{Name: name, Source: SkillSourceModel}}
}

// skillsFromReadTool returns the skill a file-read tool call loads, if it reads a SKILL.md
// from its first line. A read with an offset past line 1 continues an earlier load.
func skillsFromReadTool(input map[string]interface{}) []SkillInvocation {
	if offset, ok := input["offset"].(float64); ok && offset > 1 {
		return nil
	}
	for _, key := range []string{"path", "file_path"} {
		if skill, ok := skillFromFile(stringInput(input, key), SkillSourceModel); ok {
			return []SkillInvocation{skill}
		}
	}
	return nil
}

var piSkillBlock = regexp.MustCompile(`^<skill name="([^"]+)"(?: location="([^"]*)")?`)

// userSkillFromPiPrompt returns the skill Pi expands from a /skill:name command.
func userSkillFromPiPrompt(prompt string) []SkillInvocation {
	match := piSkillBlock.FindStringSubmatch(strings.TrimSpace(prompt))
	if match == nil {
		return nil
	}
	skill := SkillInvocation{Name: strings.TrimSpace(match[1]), Source: SkillSourceUser}
	if len(match) > 2 {
		skill.Path = strings.TrimSpace(match[2])
	}
	return []SkillInvocation{skill}
}

const claudeSkillBasePrefix = "Base directory for this skill: "

var claudeCommandName = regexp.MustCompile(`<command-name>/([^<\s]+)</command-name>`)

// claudeCommandFromPrompt returns the slash command a Claude Code user record runs.
func claudeCommandFromPrompt(prompt string) string {
	if match := claudeCommandName.FindStringSubmatch(prompt); match != nil {
		return match[1]
	}
	return ""
}

// claudeSkillDirFromMeta returns the skill directory a Claude Code skill-content message
// announces, or "" when the text is not skill content.
func claudeSkillDirFromMeta(text string) string {
	if !strings.HasPrefix(text, claudeSkillBasePrefix) {
		return ""
	}
	line, _, _ := strings.Cut(text[len(claudeSkillBasePrefix):], "\n")
	return strings.TrimSpace(line)
}

// shellSkillFields expands literal shell arguments without consulting the filesystem,
// environment, or executing substitutions from recorded commands.
func shellSkillFields(command string) []string {
	var fields []string
	for word, err := range syntax.NewParser().WordsSeq(strings.NewReader(command)) {
		if err != nil {
			return nil
		}
		dynamic := false
		syntax.Walk(word, func(node syntax.Node) bool {
			switch node.(type) {
			case *syntax.ParamExp, *syntax.CmdSubst, *syntax.ArithmExp, *syntax.ProcSubst:
				dynamic = true
			}
			return true
		})
		if dynamic {
			return nil
		}
		syntax.SplitBraces(word)
		for expanded, err := range expand.BracesSeq(nil, word) {
			if err != nil {
				return nil
			}
			value, err := expand.Literal(&expand.Config{Env: expand.ListEnviron()}, expanded)
			if err != nil {
				return nil
			}
			fields = append(fields, value)
		}
	}
	return fields
}
