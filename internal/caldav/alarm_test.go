package caldav

import (
	"reflect"
	"strings"
	"testing"
	"time"
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

func TestApplyEditAlarms(t *testing.T) {
	raw := "BEGIN:VCALENDAR\r\nBEGIN:VTODO\r\nUID:u\r\nSUMMARY:x\r\n" +
		"BEGIN:VALARM\r\nACTION:AUDIO\r\nREPEAT:5\r\nDURATION:PT1M\r\nTRIGGER;RELATED=END:PT0S\r\nEND:VALARM\r\n" +
		"BEGIN:VALARM\r\nACTION:DISPLAY\r\nTRIGGER;RELATED=END:P1D\r\nEND:VALARM\r\n" +
		"BEGIN:VALARM\r\nACTION:DISPLAY\r\nTRIGGER;VALUE=DATE-TIME:20261201T120000Z\r\nEND:VALARM\r\n" +
		"END:VTODO\r\nEND:VCALENDAR\r\n"
	want := []Alarm{{End: true}, {Offset: -10 * time.Minute, End: true}, {Offset: -90 * time.Minute}}
	out, err := ApplyEdit(raw, Edit{Alarms: &want}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{
		"REPEAT:5",                                 // still-wanted alarm kept verbatim
		"TRIGGER;RELATED=END:-PT10M",               // new
		"TRIGGER;RELATED=START:-PT1H30M",           // new
		"TRIGGER;VALUE=DATE-TIME:20261201T120000Z", // absolute untouched
	} {
		if !strings.Contains(out, s) {
			t.Errorf("missing %q in:\n%s", s, out)
		}
	}
	if strings.Contains(out, "P1D") {
		t.Errorf("dropped alarm still present:\n%s", out)
	}
	got := Task{Raw: out}.RelativeAlarms()
	if len(got) != 3 {
		t.Errorf("relative alarms = %v", got)
	}
	none := []Alarm{}
	out, _ = ApplyEdit(out, Edit{Alarms: &none}, time.Now())
	if n := len(Task{Raw: out}.RelativeAlarms()); n != 0 || !strings.Contains(out, "20261201T120000Z") {
		t.Errorf("clear failed:\n%s", out)
	}
}

func TestAlarmTriggerRoundTrip(t *testing.T) {
	for _, a := range []Alarm{{}, {End: true}, {Offset: -10 * time.Minute}, {Offset: 24 * time.Hour, End: true},
		{Offset: -26 * time.Hour}, {Offset: 90 * time.Second}} {
		got, _, ok := parseTrigger(a.trigger())
		if !ok || got != a {
			t.Errorf("%v -> %q -> %v ok=%v", a, a.trigger(), got, ok)
		}
	}
}
