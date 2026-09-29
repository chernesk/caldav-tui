// Package notify sends desktop notifications over the freedesktop D-Bus
// interface (KDE Plasma, GNOME, dunst, ...) and reports button clicks.
package notify

import (
	"html"
	"slices"
	"sync"

	"github.com/godbus/dbus/v5"
)

const (
	iface = "org.freedesktop.Notifications"
	path  = "/org/freedesktop/Notifications"
)

// Action is a button on a notification.
type Action struct{ Key, Label string }

// Event is a button click (Action set) or a dismissal (Closed set).
type Event struct {
	ID     uint32
	Action string
	Closed bool
}

type Notifier struct {
	conn    *dbus.Conn
	obj     dbus.BusObject
	events  chan Event
	actions bool // server supports buttons
	once    sync.Once
}

// New connects to the session bus and starts listening for clicks.
func New() (*Notifier, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, err
	}
	n := &Notifier{conn: conn, obj: conn.Object(iface, path), events: make(chan Event, 16)}

	var caps []string
	if err := n.obj.Call(iface+".GetCapabilities", 0).Store(&caps); err != nil {
		conn.Close()
		return nil, err // no notification server on the bus
	}
	n.actions = slices.Contains(caps, "actions")

	if err := conn.AddMatchSignal(dbus.WithMatchInterface(iface), dbus.WithMatchObjectPath(path)); err != nil {
		conn.Close()
		return nil, err
	}
	sig := make(chan *dbus.Signal, 16)
	conn.Signal(sig)
	go func() {
		for s := range sig {
			switch s.Name {
			case iface + ".ActionInvoked":
				if len(s.Body) >= 2 {
					id, _ := s.Body[0].(uint32)
					key, _ := s.Body[1].(string)
					n.emit(Event{ID: id, Action: key})
				}
			case iface + ".NotificationClosed":
				if len(s.Body) >= 1 {
					id, _ := s.Body[0].(uint32)
					n.emit(Event{ID: id, Closed: true})
				}
			}
		}
	}()
	return n, nil
}

func (n *Notifier) emit(e Event) {
	select {
	case n.events <- e:
	default: // nobody's listening; drop rather than block D-Bus dispatch
	}
}

// Events delivers clicks and dismissals.
func (n *Notifier) Events() <-chan Event { return n.events }

// Send shows a notification and returns its id. Buttons are dropped on servers
// that don't support them. Text is escaped because most servers render markup.
func (n *Notifier) Send(title, body string, actions []Action) (uint32, error) {
	var flat []string
	if n.actions {
		for _, a := range actions {
			flat = append(flat, a.Key, a.Label)
		}
	}
	hints := map[string]dbus.Variant{
		"urgency":       dbus.MakeVariant(byte(1)),
		"category":      dbus.MakeVariant("reminder"),
		"desktop-entry": dbus.MakeVariant("caldav-tui"),
	}
	var id uint32
	err := n.obj.Call(iface+".Notify", 0, "caldav-tui", uint32(0), "task-due",
		html.EscapeString(title), html.EscapeString(body), flat, hints, int32(-1)).Store(&id)
	return id, err
}

// Close withdraws a notification.
func (n *Notifier) Close(id uint32) {
	n.obj.Call(iface+".CloseNotification", dbus.FlagNoReplyExpected, id)
}

func (n *Notifier) Shutdown() { n.once.Do(func() { n.conn.Close() }) }
