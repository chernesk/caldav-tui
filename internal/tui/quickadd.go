package tui

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type quickAction int

const (
	quickNone quickAction = iota
	quickCancel
	quickSubmit
	quickExpand // open the full form with what's been understood so far
)

// quickBox is the one-line add prompt.
type quickBox struct {
	input       textinput.Model
	parent      string // parent UID when adding a subtask
	parentTitle string
	calendar    string
	defPrio     int // priority for new tasks that don't say
}

func newQuickBox(parent, parentTitle, calendar string, w, defPrio int) *quickBox {
	in := textinput.New()
	in.Prompt = "❯ "
	in.Placeholder = "call mom fri 5pm !high"
	in.CharLimit = 500
	in.Width = max(20, min(w-10, 90))
	in.Focus()
	return &quickBox{input: in, parent: parent, parentTitle: parentTitle, calendar: calendar, defPrio: defPrio}
}

func (q *quickBox) update(msg tea.Msg) (quickAction, tea.Cmd) {
	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.String() {
		case "esc", "ctrl+c":
			return quickCancel, nil
		case "enter", "ctrl+s":
			return quickSubmit, nil
		case "tab":
			return quickExpand, nil
		}
	}
	var cmd tea.Cmd
	q.input, cmd = q.input.Update(msg)
	return quickNone, cmd
}

func (q *quickBox) view(now time.Time) string {
	var b strings.Builder
	if q.parent != "" {
		b.WriteString(title.Render("New subtask of “" + truncate(q.parentTitle, 40) + "”"))
	} else {
		b.WriteString(title.Render("Quick add"))
	}
	b.WriteString("\n\n" + q.input.View() + "\n\n")

	if txt := strings.TrimSpace(q.input.Value()); txt != "" {
		if p, err := QuickParse(txt, now); err != nil {
			b.WriteString(dim.Render("  " + err.Error()))
		} else {
			b.WriteString("  → " + p.WithDefault(q.defPrio).Describe())
		}
	}
	b.WriteString("\n\n")
	b.WriteString(dim.Render(strings.Join([]string{
		"  call mom fri 5pm !high",
		"  water plants every monday 9am",
		"  pay rent by oct 1 // via the app",
		"  plan trip start:oct 5 due:oct 20",
		"  call dentist fri 2pm alarm:1 hour before alarm:at due",
		"",
		"  !high/!med/!low priority · every week/2 weeks/mon,wed · alarm:10 min before · // notes · \\word keeps a word literal",
	}, "\n")))
	b.WriteString("\n\n" + dim.Render("enter add · tab full form · esc cancel"))
	return lipgloss.NewStyle().Padding(1, 2).Render(b.String())
}

// expandForm opens the full form pre-filled from whatever the prompt understood.
func (q *quickBox) expandForm(now time.Time, w int) *form {
	f := newForm(nil, q.parent, q.parentTitle, q.calendar, w, q.defPrio)
	raw := strings.TrimSpace(q.input.Value())
	if raw == "" {
		return f
	}
	p, err := QuickParse(raw, now)
	if err != nil {
		f.inputs[fTitle].SetValue(raw) // couldn't parse: keep the text, let the user fix it
		return f
	}
	p = p.WithDefault(q.defPrio)
	f.inputs[fTitle].SetValue(p.Title)
	f.inputs[fDue].SetValue(FormatWhen(p.Due))
	f.inputs[fStart].SetValue(FormatWhen(p.Start))
	f.inputs[fPrio].SetValue(PriorityText(p.Priority))
	f.inputs[fRepeat].SetValue(RepeatText(p.RRule))
	f.inputs[fAlarm].SetValue(AlarmsText(p.Alarms))
	f.notes.SetValue(p.Notes)
	return f
}
