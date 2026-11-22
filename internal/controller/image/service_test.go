package image

import "testing"

func TestImageVisibility(t *testing.T) {
	cases := []struct {
		name    string
		img     Image
		project string
		public  bool
		visible bool
	}{
		{"legacy image (no owner, no visibility) stays public", Image{}, "p1", true, true},
		{"public platform image", Image{Visibility: VisibilityPublic}, "p1", true, true},
		{"private image, owning project", Image{ProjectID: "p1", Visibility: VisibilityPrivate}, "p1", false, true},
		{"private image, other project", Image{ProjectID: "p1", Visibility: VisibilityPrivate}, "p2", false, false},
		{"private image, no project", Image{ProjectID: "p1", Visibility: VisibilityPrivate}, "", false, false},
		{"owned image without visibility is private", Image{ProjectID: "p1"}, "p2", false, false},
	}
	for _, c := range cases {
		if got := c.img.IsPublic(); got != c.public {
			t.Errorf("%s: IsPublic = %v", c.name, got)
		}
		if got := c.img.VisibleTo(c.project); got != c.visible {
			t.Errorf("%s: VisibleTo(%q) = %v", c.name, c.project, got)
		}
	}
}
