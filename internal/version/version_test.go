package version

import (
	"runtime/debug"
	"testing"
)

func TestFromInfo(t *testing.T) {
	mk := func(v string, kv ...string) *debug.BuildInfo {
		bi := &debug.BuildInfo{Main: debug.Module{Version: v}}
		for i := 0; i < len(kv); i += 2 {
			bi.Settings = append(bi.Settings, debug.BuildSetting{Key: kv[i], Value: kv[i+1]})
		}
		return bi
	}
	cases := []struct {
		bi   *debug.BuildInfo
		want string
	}{
		{mk("v0.10.0"), "0.10.0"},
		{mk("(devel)", "vcs.revision", "0ff4109abcdef", "vcs.modified", "true"), "dev-0ff4109+dirty"},
		{mk("", "vcs.revision", "0ff4109abcdef", "vcs.modified", "false"), "dev-0ff4109"},
		{mk("(devel)"), "dev"},
	}
	for _, c := range cases {
		if got := fromInfo(c.bi); got != c.want {
			t.Errorf("got %q want %q", got, c.want)
		}
	}
}
