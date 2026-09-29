package auth

import "testing"

func TestPassword(t *testing.T) {
	h, err := Hash("a long test password")
	if err != nil {
		t.Fatal(err)
	}
	if !Verify(h, "a long test password") || Verify(h, "wrong") {
		t.Fatal("password verification failed")
	}
	for _, bad := range []string{"", "$argon2id$v=19$m=999999999,t=3,p=1$a$b", prefix + "bad"} {
		if Verify(bad, "test") {
			t.Fatal("accepted invalid hash")
		}
	}
	if _, err := Hash("short"); err == nil {
		t.Fatal("accepted short password")
	}
}
