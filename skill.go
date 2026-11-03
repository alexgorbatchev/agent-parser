package parser

import (
	"path"
	"regexp"
	"strings"
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

// skillFromFile returns the skill whose definition file is filePath. Only files named
// SKILL.md directly inside a directory under a "skills" directory qualify, which is how
// every supported harness lays out installed skills.
func skillFromFile(filePath, source string) (SkillInvocation, bool) {
	clean := path.Clean(strings.ReplaceAll(filePath, "\\", "/"))
	if path.Base(clean) != skillFileName {
		return SkillInvocation{}, false
	}
	dir := path.Dir(clean)
	name := path.Base(dir)
	if path.Base(path.Dir(dir)) != "skills" || name == "." || name == "/" {
		return SkillInvocation{}, false
	}
	return SkillInvocation{Name: name, Source: source, Path: clean}, true
}

// shellReadCommands are commands that only read the files they are given.
var shellReadCommands = map[string]bool{
	"cat": true, "sed": true, "head": true, "tail": true, "nl": true, "bat": true, "less": true, "more": true,
}

var shellSeparator = regexp.MustCompile(`&&|\|\||[;|\n]`)

// skillsFromShellCommand returns the skills whose SKILL.md a shell script reads. Only
// simple commands that read files and redirect nothing count, so edits, listings, and
// commands that merely mention a SKILL.md path are ignored. Each skill is reported once.
func skillsFromShellCommand(script string) []SkillInvocation {
	var skills []SkillInvocation
	seen := map[string]bool{}
	for _, segment := range shellSeparator.Split(script, -1) {
		fields := strings.Fields(segment)
		if len(fields) == 0 || !shellReadCommands[path.Base(trimShellQuotes(fields[0]))] {
			continue
		}
		if strings.ContainsAny(segment, "<>") || editsInPlace(fields) {
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

// skillsFromReadTool returns the skill a file-read tool call loads, if it reads a SKILL.md.
func skillsFromReadTool(input map[string]interface{}) []SkillInvocation {
	for _, key := range []string{"path", "file_path"} {
		if skill, ok := skillFromFile(stringInput(input, key), SkillSourceModel); ok {
			return []SkillInvocation{skill}
		}
	}
	return nil
}

var (
	codexSkillBlock = regexp.MustCompile(`^<skill>\s*<name>([^<]+)</name>\s*<path>([^<]*)</path>`)
	piSkillBlock    = regexp.MustCompile(`^<skill name="([^"]+)"(?: location="([^"]*)")?`)
)

// userSkillFromCodexPrompt returns the skill Codex injects as a user message when the user
// mentions it with $name.
func userSkillFromCodexPrompt(prompt string) []SkillInvocation {
	return userSkillFromBlock(codexSkillBlock, prompt)
}

// userSkillFromPiPrompt returns the skill Pi expands from a /skill:name command.
func userSkillFromPiPrompt(prompt string) []SkillInvocation {
	return userSkillFromBlock(piSkillBlock, prompt)
}

func userSkillFromBlock(block *regexp.Regexp, prompt string) []SkillInvocation {
	match := block.FindStringSubmatch(strings.TrimSpace(prompt))
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
