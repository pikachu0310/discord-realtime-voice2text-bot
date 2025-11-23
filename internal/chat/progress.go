package chat

import (
	"strings"
	"sync"
)

type progressBuilder struct {
	model    string
	input    string
	steps    []string
	final    string
	done     bool
	verbose  bool
	mu       sync.Mutex
	OnUpdate func(string)
}

func newProgress(model string) *progressBuilder {
	return &progressBuilder{
		model: model,
		steps: []string{},
	}
}

func (p *progressBuilder) WithInput(input string) *progressBuilder {
	p.input = strings.TrimSpace(input)
	return p
}

func (p *progressBuilder) AddStep(step string) {
	step = strings.TrimSpace(step)
	if step == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.steps = append(p.steps, step)
}

func (p *progressBuilder) SetFinal(final string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.final = strings.TrimSpace(final)
	p.done = true
}

func (p *progressBuilder) SetVerbose(verbose bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.verbose = verbose
}

func (p *progressBuilder) Render() string {
	p.mu.Lock()
	defer p.mu.Unlock()

	var sections []string
	const quietLimit = 500

	var body []string
	if p.input != "" {
		body = append(body, "「"+p.input+"」")
	}
	if p.final != "" {
		body = append(body, p.final)
	}

	var stepLines []string
	for _, step := range p.steps {
		if strings.TrimSpace(step) == "" {
			continue
		}
		stepLines = append(stepLines, step)
	}
	if len(stepLines) > 0 {
		if p.verbose || !p.done {
			if p.verbose {
				sections = append(sections, strings.Join(stepLines, "\n"))
			} else {
				line := stepLines[len(stepLines)-1]
				if runeLen(line) > quietLimit {
					line = truncateWithEllipsis(line, quietLimit)
				}
				sections = append(sections, line)
			}
		}
	}

	if len(body) > 0 {
		sections = append(sections, strings.Join(body, "\n"))
	}

	out := strings.Join(sections, "\n\n")
	if out == "" {
		return ""
	}
	return out
}

func runeLen(s string) int {
	return len([]rune(s))
}

func truncateWithEllipsis(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "..."
}
