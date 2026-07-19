package email

import (
	"strings"
	"testing"
)

// The two emails this product ever sends. Both exist to deliver one six-digit code, and if that
// code does not arrive intact the person cannot create an account or get back into one.

func TestVerificationEmailCarriesTheCode(t *testing.T) {
	subject, text, html := VerificationEmail("482913")

	if subject == "" {
		t.Error("no subject; some clients show the first line of the body instead")
	}
	if !strings.Contains(text, "482913") {
		t.Errorf("the plain-text body does not contain the code:\n%s", text)
	}
	if !strings.Contains(html, "482913") {
		t.Errorf("the HTML body does not contain the code:\n%s", html)
	}
	// Both bodies are required: a client that refuses HTML must still be able to show the code,
	// and one that prefers HTML must not fall back to an empty alternative.
	if strings.TrimSpace(text) == "" || strings.TrimSpace(html) == "" {
		t.Error("one of the two bodies is empty")
	}
}

func TestPasswordResetEmailCarriesTheCodeAndSaysWhatItIsFor(t *testing.T) {
	subject, text, html := PasswordResetEmail("100200")

	if !strings.Contains(text, "100200") || !strings.Contains(html, "100200") {
		t.Error("the reset code is missing from a body")
	}
	// The two emails must not be confusable. Somebody who receives a reset code they did not ask
	// for needs to be able to tell that is what it is.
	verifySubject, verifyText, _ := VerificationEmail("100200")
	if subject == verifySubject {
		t.Errorf("the reset email and the verification email share the subject %q; a person cannot "+
			"tell which one they are looking at", subject)
	}
	if strings.EqualFold(strings.TrimSpace(text), strings.TrimSpace(verifyText)) {
		t.Error("the reset and verification bodies are identical")
	}
	if !strings.Contains(strings.ToLower(subject+text), "password") {
		t.Errorf("nothing in the reset email mentions a password:\nsubject: %s\n%s", subject, text)
	}
}

// Both emails tell the reader what to do if they did not ask for it. An unexpected code with no
// explanation reads as a compromised account.
func TestBothEmailsTellAnUnexpectedRecipientWhatToDo(t *testing.T) {
	for name, body := range map[string]string{
		"verification": mustText(VerificationEmail("111111")),
		"reset":        mustText(PasswordResetEmail("222222")),
	} {
		if !strings.Contains(strings.ToLower(body), "ignore") {
			t.Errorf("the %s email does not tell an unexpected recipient they can ignore it:\n%s",
				name, body)
		}
	}
}

// The code is escaped into the HTML. Nothing can put markup there today — the code is six digits
// from the OTP generator — so this is about the template staying safe if it is ever reused.
func TestTheHTMLBodyEscapesWhatItInterpolates(t *testing.T) {
	_, _, html := VerificationEmail(`<script>alert(1)</script>`)

	if strings.Contains(html, "<script>") {
		t.Errorf("markup passed as a code was interpolated into the HTML body unescaped:\n%s", html)
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Errorf("the value was neither escaped nor present; something else happened to it:\n%s", html)
	}
}

// The HTML is a complete document. A fragment renders as raw markup in some clients.
func TestTheHTMLBodyIsAWholeDocument(t *testing.T) {
	_, _, html := VerificationEmail("333333")

	for _, want := range []string{"<!doctype html>", "<html", "<body", "</html>"} {
		if !strings.Contains(strings.ToLower(html), want) {
			t.Errorf("the HTML body is missing %q; some clients render a fragment as raw markup", want)
		}
	}
}

func mustText(_ string, text string, _ string) string { return text }
