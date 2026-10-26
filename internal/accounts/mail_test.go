package accounts

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalMailTransport(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "mail")
	env := func(k string) string {
		if k == "MAIL_DIRECTORY" {
			return dir
		}
		return ""
	}
	if _, err := MailSenderFromEnv(env, "https://quiz.example"); err == nil {
		t.Fatal("file transport allowed for public origin")
	}
	send, err := MailSenderFromEnv(env, "http://localhost:5175")
	if err != nil {
		t.Fatal(err)
	}
	if err = send(context.Background(), "test@example.com", "Тест", "Письмо"); err != nil {
		t.Fatal(err)
	}
	files, _ := os.ReadDir(dir)
	if len(files) != 1 {
		t.Fatal("mail not saved")
	}
	path := filepath.Join(dir, files[0].Name())
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("mail permissions")
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "Письмо") {
		t.Fatal("body missing")
	}
}
