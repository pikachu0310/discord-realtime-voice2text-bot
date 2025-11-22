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
}

func (p *progressBuilder) Render() string {
	p.mu.Lock()
	defer p.mu.Unlock()

	var b strings.Builder

	if p.input != "" {
		b.WriteString("入力: ")
		b.WriteString(p.input)
		b.WriteString("\n")
	}
	b.WriteString(p.model)
	b.WriteString(" が考え中...")

	for _, step := range p.steps {
		b.WriteString("\n")
		b.WriteString(step)
	}
	if p.final != "" {
		b.WriteString("\n\n")
		b.WriteString(p.final)
	}
	return b.String()
}
