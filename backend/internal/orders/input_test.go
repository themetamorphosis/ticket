package orders

import (
	"bytes"
	"errors"
	"regexp"
	"strings"
	"testing"

	"kacheem/backend/internal/catalog"
)

var event = catalog.Event{MaxPerOrder: 4, MinAge: 16}

func valid() Input {
	return Input{Quantity: 3, Name: "  Nusrat   Jahan ", Email: " nusrat@example.com ", Phone: "+880 1711-000000", Age: 28}
}

func TestNormalizeCanonicalizes(t *testing.T) {
	got, err := Normalize(event, valid())
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	want := Input{Quantity: 3, Name: "Nusrat Jahan", Email: "nusrat@example.com", Phone: "+8801711000000", Age: 28}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestNormalizeRejectsEachField(t *testing.T) {
	cases := []struct {
		field  string
		mutate func(*Input)
	}{
		{"quantity", func(in *Input) { in.Quantity = 0 }},
		{"quantity", func(in *Input) { in.Quantity = 5 }},
		{"name", func(in *Input) { in.Name = "   " }},
		{"name", func(in *Input) { in.Name = strings.Repeat("a", 101) }},
		{"name", func(in *Input) { in.Name = "Bad\u0007Name" }},
		{"email", func(in *Input) { in.Email = "not-an-email" }},
		{"email", func(in *Input) { in.Email = "Nusrat <nusrat@example.com>" }},
		{"email", func(in *Input) { in.Email = "nusrat@localhost" }},
		{"email", func(in *Input) { in.Email = strings.Repeat("a", 250) + "@example.com" }},
		{"phone", func(in *Input) { in.Phone = "01711000000" }},
		{"phone", func(in *Input) { in.Phone = "+0123456789" }},
		{"age", func(in *Input) { in.Age = 15 }},
		{"age", func(in *Input) { in.Age = 121 }},
	}
	for _, c := range cases {
		in := valid()
		c.mutate(&in)
		_, err := Normalize(event, in)
		var fe FieldErrors
		if !errors.As(err, &fe) || fe[c.field] == "" || len(fe) != 1 {
			t.Errorf("%s case %+v: err = %v", c.field, in, err)
		}
	}
}

func TestNormalizeReportsAllFields(t *testing.T) {
	_, err := Normalize(event, Input{})
	var fe FieldErrors
	if !errors.As(err, &fe) || len(fe) != 5 || err.Error() != "invalid booking details" {
		t.Fatalf("err = %v (%v)", err, fe)
	}
}

func TestDigestBindsEventAndContents(t *testing.T) {
	in, _ := Normalize(event, valid())
	a, b := digest("live", in), digest("live", in)
	if !bytes.Equal(a, b) || len(a) != 32 {
		t.Fatal("digest not deterministic")
	}
	other := in
	other.Quantity = 2
	if bytes.Equal(a, digest("live", other)) || bytes.Equal(a, digest("other", in)) {
		t.Fatal("digest ignores contents")
	}
}

func TestAllocate(t *testing.T) {
	shards := []shardFree{{0, 2}, {1, 0}, {2, 5}, {3, 1}}
	cases := []struct {
		qty, start int
		want       []part
	}{
		{1, 0, []part{{0, 1}}},
		{3, 0, []part{{0, 2}, {2, 1}}},
		{3, 2, []part{{2, 3}}},
		{7, 3, []part{{0, 2}, {2, 4}, {3, 1}}},
		{8, 1, []part{{0, 2}, {2, 5}, {3, 1}}},
		{9, 0, nil},
	}
	for _, c := range cases {
		got := allocate(shards, c.qty, c.start)
		if len(got) != len(c.want) {
			t.Errorf("allocate(%d, %d) = %v, want %v", c.qty, c.start, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("allocate(%d, %d) = %v, want %v", c.qty, c.start, got, c.want)
			}
		}
	}
	if allocate(nil, 1, 0) != nil || totalFree(shards) != 8 {
		t.Fatal("empty or total wrong")
	}
}

func TestNewRef(t *testing.T) {
	re := regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{5}-[0-9A-HJKMNP-TV-Z]{5}$`)
	seen := map[string]bool{}
	for range 1000 {
		r := newRef()
		if !re.MatchString(r) || seen[r] {
			t.Fatalf("bad or repeated ref %q", r)
		}
		seen[r] = true
	}
}
