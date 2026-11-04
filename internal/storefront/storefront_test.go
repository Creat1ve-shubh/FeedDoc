package storefront

import "testing"

func TestContacts(t *testing.T) {
	cases := []struct{ html, mail, phone string }{
		{`<a href="mailto:hi@shop.com">Email</a>`, "hi@shop.com", ""},
		{`<img src="/logo@2x.png"> write to care@shop.com`, "care@shop.com", ""},
		{`<a href="tel:+91 98765 43210">Call</a>`, "", "+91 98765 43210"},
		{`<form method="post" action="/contact#ContactForm" id="ContactForm"><input type="hidden" name="form_type" value="contact">`, "contact form", ""},
		{`<footer>Follow us</footer>`, "", ""},
	}
	for _, c := range cases {
		mail, phone := contacts(c.html)
		if mail != c.mail || phone != c.phone {
			t.Errorf("contacts(%q) = %q, %q; want %q, %q", c.html, mail, phone, c.mail, c.phone)
		}
	}
}
