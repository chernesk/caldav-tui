package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/chernesk/caldav-tui/internal/caldav"
)

type fieldID int

const (
	fTitle fieldID = iota
	fDue
	fStart
	fPrio
	fRepeat
	fAlarm
	fNotes
	nFields
)

var fieldLabels = [nFields]string{"Title", "Due", "Start", "Priority", "Repeat", "Alarm", "Notes"}

var fieldHints = [nFields]string{
	"",
	"today, tomorrow 2pm, fri, +3d, oct 5, 2026-10-05 14:30",
	"same formats as due; hidden from the list until this day",
	"high, medium, low, or none for no priority",
	"daily, weekly mon,wed, every 2 weeks, monthly, weekdays",
	"at due, 10 min before, 1 day after due, at start; comma-separated",
	"",
}

type formAction int

const (
	formNone formAction = iota
	formCancel
	formSubmit
)

// form edits one task: an existing one (task != nil) or a new one.
type form struct {
	inputs   [fNotes]textinput.Model
	notes    textarea.Model
	focus    fieldID
	task     *caldav.Task // nil when adding
	parent   string       // parent UID when adding a subtask
	parentOf string       // parent's title, for the header
	calendar string       // calendar path for new tasks
	orig     [nFields]string
	err      string
	w        int
}

// newForm builds the add/edit form. defPrio pre-fills the priority of new tasks.
func newForm(t *caldav.Task, parent, parentTitle, calendar string, w, defPrio int) *form {
	f := &form{task: t, parent: parent, parentOf: parentTitle, calendar: calendar, w: w}
	for i := range f.inputs {
		in := textinput.New()
		in.Prompt = ""
		in.CharLimit = 500
		f.inputs[i] = in
	}
	f.notes = textarea.New()
	f.notes.Prompt = ""
	f.notes.ShowLineNumbers = false
	f.notes.SetHeight(5)
	f.notes.CharLimit = 0

	if t != nil {
		f.orig[fTitle] = t.Summary
		if !t.Due.IsZero() {
			f.orig[fDue] = FormatWhen(caldav.When{T: t.Due, HasTime: !t.DueAllDay})
		}
		if !t.Start.IsZero() {
			f.orig[fStart] = FormatWhen(caldav.When{T: t.Start, HasTime: !t.StartAllDay})
		}
		f.orig[fPrio] = PriorityText(t.Priority)
		f.orig[fRepeat] = RepeatText(t.RRule)
		f.orig[fAlarm] = AlarmsText(t.RelativeAlarms())
		f.orig[fNotes] = t.Notes
	}
	for i := range f.inputs {
		f.inputs[i].SetValue(f.orig[i])
	}
	if t == nil && defPrio != 0 {
		// orig stays empty, so the pre-filled default counts as a change and is saved
		f.inputs[fPrio].SetValue(PriorityText(defPrio))
	}
	f.notes.SetValue(f.orig[fNotes])
	f.resize(w)
	f.setFocus(fTitle)
	return f
}

func (f *form) resize(w int) {
	f.w = w
	iw := max(20, min(w-16, 70))
	for i := range f.inputs {
		f.inputs[i].Width = iw
	}
	f.notes.SetWidth(iw + 2)
}

func (f *form) setFocus(id fieldID) {
	f.focus = id
	for i := range f.inputs {
		if fieldID(i) == id {
			f.inputs[i].Focus()
		} else {
			f.inputs[i].Blur()
		}
	}
	if id == fNotes {
		f.notes.Focus()
	} else {
		f.notes.Blur()
	}
}

func (f *form) text(id fieldID) string {
	if id == fNotes {
		return f.notes.Value()
	}
	return f.inputs[id].Value()
}

func (f *form) update(msg tea.Msg) (formAction, tea.Cmd) {
	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.String() {
		case "esc", "ctrl+c":
			return formCancel, nil
		case "ctrl+s":
			return formSubmit, nil
		case "tab":
			f.setFocus((f.focus + 1) % nFields)
			return formNone, nil
		case "shift+tab":
			f.setFocus((f.focus + nFields - 1) % nFields)
			return formNone, nil
		case "enter":
			if f.focus != fNotes {
				return formSubmit, nil
			}
		case "down":
			if f.focus != fNotes {
				f.setFocus(f.focus + 1)
				return formNone, nil
			}
		case "up":
			if f.focus != fNotes && f.focus != fTitle {
				f.setFocus(f.focus - 1)
				return formNone, nil
			}
		}
		f.err = ""
	}
	var cmd tea.Cmd
	if f.focus == fNotes {
		f.notes, cmd = f.notes.Update(msg)
	} else {
		f.inputs[f.focus], cmd = f.inputs[f.focus].Update(msg)
	}
	return formNone, cmd
}

// edit validates the form and returns the changes. Fields left as they were
// are nil, so an untouched due date keeps its original timezone form.
func (f *form) edit(now time.Time) (caldav.Edit, error) {
	var e caldav.Edit
	changed := func(id fieldID) bool { return f.text(id) != f.orig[id] }

	title := strings.TrimSpace(f.text(fTitle))
	if title == "" {
		f.setFocus(fTitle)
		return e, fmt.Errorf("title is required")
	}
	if changed(fTitle) {
		e.Summary = &title
	}
	for _, d := range []struct {
		id  fieldID
		dst **caldav.When
	}{{fDue, &e.Due}, {fStart, &e.Start}} {
		if !changed(d.id) {
			continue
		}
		w, err := ParseWhen(f.text(d.id), now)
		if err != nil {
			f.setFocus(d.id)
			return e, fmt.Errorf("%s: %w", strings.ToLower(fieldLabels[d.id]), err)
		}
		*d.dst = &w
	}
	if changed(fPrio) {
		p, err := ParsePriority(f.text(fPrio))
		if err != nil {
			f.setFocus(fPrio)
			return e, err
		}
		e.Priority = &p
	}
	if changed(fRepeat) {
		r, err := ParseRepeat(f.text(fRepeat))
		if err != nil {
			f.setFocus(fRepeat)
			return e, err
		}
		e.RRule = &r
	}
	if changed(fAlarm) {
		as, err := ParseAlarms(f.text(fAlarm))
		if err != nil {
			f.setFocus(fAlarm)
			return e, err
		}
		e.Alarms = &as
	}
	if changed(fNotes) {
		n := strings.TrimRight(f.text(fNotes), " \n\t")
		e.Notes = &n
	}
	return e, nil
}

func (f *form) view(now time.Time) string {
	var b strings.Builder
	switch {
	case f.task != nil:
		b.WriteString(title.Render("Edit task"))
	case f.parent != "":
		b.WriteString(title.Render("New subtask of “" + truncate(f.parentOf, 40) + "”"))
	default:
		b.WriteString(title.Render("New task"))
	}
	b.WriteString("\n\n")

	for i := fieldID(0); i < fNotes; i++ {
		label := fmt.Sprintf("%-9s", fieldLabels[i])
		if i == f.focus {
			label = bold.Render(label)
		} else {
			label = dim.Render(label)
		}
		b.WriteString(label + f.inputs[i].View() + "\n")
		b.WriteString(strings.Repeat(" ", 9) + f.note(i, now) + "\n")
	}
	lab := fmt.Sprintf("%-9s", fieldLabels[fNotes])
	if f.focus == fNotes {
		lab = bold.Render(lab)
	} else {
		lab = dim.Render(lab)
	}
	b.WriteString(lab + "\n")
	b.WriteString(f.notes.View() + "\n\n")

	if f.err != "" {
		b.WriteString(errStyle.Render(f.err) + "\n")
	} else {
		b.WriteString("\n")
	}
	b.WriteString(dim.Render("enter/ctrl+s save · tab next field · esc cancel"))
	return lipgloss.NewStyle().Padding(1, 2).Render(b.String())
}

// note is the line under a field: what we understood, or a hint while empty.
func (f *form) note(id fieldID, now time.Time) string {
	txt := strings.TrimSpace(f.text(id))
	if txt == "" {
		if id == f.focus {
			return dim.Render(fieldHints[id])
		}
		return ""
	}
	switch id {
	case fDue, fStart:
		w, err := ParseWhen(txt, now)
		if err != nil {
			return errStyle.Render(err.Error())
		}
		if w.HasTime {
			return dim.Render("→ " + w.T.Format("Mon Jan 2, 2006 15:04"))
		}
		return dim.Render("→ " + w.T.Format("Mon Jan 2, 2006"))
	case fPrio:
		if _, err := ParsePriority(txt); err != nil {
			return errStyle.Render(err.Error())
		}
	case fRepeat:
		r, err := ParseRepeat(txt)
		if err != nil {
			return errStyle.Render(err.Error())
		}
		return dim.Render("→ " + r)
	case fAlarm:
		as, err := ParseAlarms(txt)
		if err != nil {
			return errStyle.Render(err.Error())
		}
		return dim.Render("→ " + AlarmsText(as))
	}
	return ""
}
