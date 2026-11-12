package caldav

import (
	"reflect"
	"testing"
)

func TestAlarms(t *testing.T) {
	raw := "BEGIN:VCALENDAR\r\nBEGIN:VTODO\r\nSUMMARY:x\r\n" +
		"BEGIN:VALARM\r\nACTION:DISPLAY\r\nTRIGGER;RELATED=END:PT0S\r\nEND:VALARM\r\n" +
		"BEGIN:VALARM\r\nTRIGGER;RELATED=END:P1D\r\nEND:VALARM\r\n" +
		"BEGIN:VALARM\r\nTRIGGER:-PT10M\r\nEND:VALARM\r\n" +
		"BEGIN:VALARM\r\nTRIGGER;RELATED=START:PT0S\r\nEND:VALARM\r\n" +
		"BEGIN:VALARM\r\nTRIGGER:-P1DT2H\r\nEND:VALARM\r\n" +
		"END:VTODO\r\nEND:VCALENDAR\r\n"
	got := Task{Raw: raw}.Alarms()
	want := []string{"at due", "1 day after due", "10 min before start", "at start", "1 day 2 hours before start"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q want %q", got, want)
	}
}
