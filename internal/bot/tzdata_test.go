package bot

// Embed the IANA timezone database for tests so that time.LoadLocation
// works on minimal container images (e.g., golang:alpine) that lack
// /usr/share/zoneinfo. In the production binary, internal/config provides
// this import; tests in the bot package need their own copy.
import _ "time/tzdata"
