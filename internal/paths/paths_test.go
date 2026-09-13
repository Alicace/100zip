package paths

import "testing"

func TestSanitizeEntry(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"docs/readme.txt", "docs/readme.txt", true},
		{"./docs/a.txt", "docs/a.txt", true},
		{"docs\\win.txt", "docs/win.txt", true},
		{"../etc/passwd", "", false},
		{"a/../../b", "", false},
		{"/etc/passwd", "", false},
		{"C:/Windows/x", "", false},
		{"", "", false},
		{"dir/", "dir", true},
		{"../", "", false},
	}
	for _, c := range cases {
		got, ok := SanitizeEntry(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("SanitizeEntry(%q) = (%q,%v), want (%q,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestIsVolumeTail(t *testing.T) {
	if IsVolumeTail("a.7z.001") {
		t.Error(".001 是首卷，不应判定为后续卷")
	}
	if !IsVolumeTail("a.7z.002") {
		t.Error(".002 应判定为后续卷")
	}
	if !IsVolumeTail("movie.part2.rar") {
		t.Error("part2 应判定为后续卷")
	}
	if IsVolumeTail("movie.part1.rar") {
		t.Error("part1 是首卷")
	}
	if !IsVolumeTail("data.z02") {
		t.Error(".z02 应判定为后续卷")
	}
	if !IsVolumeTail("a.7z.010") {
		t.Error(".010 应判定为后续卷")
	}
	if !IsVolumeTail("data.z10") {
		t.Error(".z10 应判定为后续卷")
	}
	if !IsVolumeTail("movie.part10.7z") {
		t.Error("part10 应判定为后续卷")
	}
	if !IsVolumeTail("movie.r00") {
		t.Error(".r00 应判定为旧式 RAR 后续卷")
	}
	if IsVolumeTail("movie.Z01") {
		t.Error(".Z01 是 WinRAR 分卷的第一段，不是后续卷")
	}
	if IsVolumeTail("movie.RAR") {
		t.Error(".RAR 是旧式分卷首卷")
	}
	if IsVolumeTail("normal.zip") {
		t.Error("普通 zip 不应判定为分卷")
	}
}

func TestGuardPrefixBoundary(t *testing.T) {
	g := &Guard{roots: []string{"/vol1/a"}}
	if !g.UnderRoot("/vol1/a") {
		t.Error("根目录本身应通过")
	}
	if !g.UnderRoot("/vol1/a/b") {
		t.Error("子路径应通过")
	}
	if g.UnderRoot("/vol1/ab") {
		t.Error("同前缀不同目录不得通过（边界比较）")
	}
	if g.UnderRoot("/vol1/other") {
		t.Error("无关目录不得通过")
	}
}
