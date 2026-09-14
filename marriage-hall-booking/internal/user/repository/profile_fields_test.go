package repository

import (
	"encoding/json"
	"testing"
)

// The /users/me body is a client contract carried over from Java's
// UserProfileDTO. These three were missing once already - the users table had
// the columns and the query simply did not select them.
func TestProfileJSONHasEveryJavaField(t *testing.T) {
	b, err := json.Marshal(Profile{})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{
		"id", "firstName", "lastName", "avatarUrl", "kycStatus", "bio", "addresses",
		"email", "phoneNumber", "status", "emailVerified", "phoneVerified",
		"memberSince", "roles",
	} {
		if _, ok := got[f]; !ok {
			t.Errorf("/users/me is missing %q, which Java's UserProfileDTO returns", f)
		}
	}
}
