package email

import (
	"fmt"
	"html"
)

// VerificationEmail returns the subject, plain-text, and HTML bodies for an
// account-verification message carrying a 6-digit code.
func VerificationEmail(code string) (subject, text, html string) {
	subject = "Your Pheme verification code"
	text = fmt.Sprintf(
		"Welcome to Pheme!\n\n"+
			"Your verification code is: %s\n\n"+
			"Enter it to finish creating your account. The code expires shortly.\n"+
			"If you didn't request this, you can ignore this email.\n",
		code)
	html = codeHTML("Verify your email", "Enter this code to finish creating your Pheme account.", code)
	return subject, text, html
}

// PasswordResetEmail returns the subject, plain-text, and HTML bodies for a
// password-reset message carrying a 6-digit code.
func PasswordResetEmail(code string) (subject, text, html string) {
	subject = "Your Pheme password reset code"
	text = fmt.Sprintf(
		"We received a request to reset your Pheme password.\n\n"+
			"Your reset code is: %s\n\n"+
			"Enter it together with your new password. The code expires shortly.\n"+
			"If you didn't request this, you can safely ignore this email.\n",
		code)
	html = codeHTML("Reset your password", "Enter this code together with your new password.", code)
	return subject, text, html
}

// codeHTML renders a minimal, inline-styled email body with a prominent code.
//
// The interpolated values are escaped. Today they cannot carry markup — the headings are constants
// in this file and the code is six digits from the OTP generator — so this changes nothing about
// what is sent. It is here because "safe as long as nobody ever passes something else" is a
// property of the callers rather than of this function, and an unescaped %s in an HTML template is
// the kind of thing that stays correct right up until someone reuses it.
func codeHTML(heading, lead, code string) string {
	heading, lead, code = html.EscapeString(heading), html.EscapeString(lead), html.EscapeString(code)
	return fmt.Sprintf(`<!doctype html>
<html>
  <body style="margin:0;padding:0;background:#f4f1fb;font-family:-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;">
    <div style="max-width:480px;margin:0 auto;padding:32px 24px;">
      <div style="background:#ffffff;border-radius:16px;padding:32px;box-shadow:0 1px 3px rgba(20,12,40,0.08);">
        <div style="font-weight:700;font-size:20px;color:#6d28d9;margin-bottom:8px;">Pheme</div>
        <h1 style="font-size:22px;color:#140c28;margin:0 0 8px;">%s</h1>
        <p style="font-size:15px;color:#52525b;margin:0 0 24px;line-height:1.5;">%s</p>
        <div style="font-size:34px;font-weight:700;letter-spacing:8px;color:#140c28;background:#f4f1fb;border-radius:12px;padding:16px;text-align:center;">%s</div>
        <p style="font-size:13px;color:#a1a1aa;margin:24px 0 0;line-height:1.5;">If you didn't request this, you can safely ignore this email.</p>
      </div>
    </div>
  </body>
</html>`, heading, lead, code)
}
