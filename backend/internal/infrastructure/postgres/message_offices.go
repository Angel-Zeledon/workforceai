package postgres

// officePred is the SQL of application.MessageOffices.Allows over the office column col of messages: asked is a bool
// placeholder, office a text one (the office asked, "" = General) and hide a text[] one (never NULL).
func officePred(col, asked, office, hide string) string {
	return `(` + asked + `::bool = false OR COALESCE(NULLIF(` + col + `, 'general'), '') = ` + office + `::text)
		AND NOT (COALESCE(` + col + `, '') = ANY(` + hide + `::text[]))`
}
