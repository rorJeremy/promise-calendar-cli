package client

// The Staff API's wire types, one per golden file in testdata/api (the
// calendar app's test/fixtures/files/api). Timestamps stay strings, such as
// "2099-10-25T18:00:00.000-05:00": a time.Time would re-encode without the
// milliseconds. Parse them when rendering. Nullable fields (per the app's
// jbuilder views and db/schema.rb) are pointers without omitempty, so a
// null re-encodes as null. golden_test.go pins all of this.

// Event is one event as GET /events/:id returns it (event.json). Its dates
// and times are the series': occurs_on and starts_at are its first
// occurrence's. All-day events have null starts_at and ends_at.
type Event struct {
	ID             string     `json:"id"`
	Title          string     `json:"title"`
	Description    *string    `json:"description"`
	InternalNotes  *string    `json:"internal_notes"`
	OccursOn       string     `json:"occurs_on"`
	EndsOn         *string    `json:"ends_on"`
	StartsAt       *string    `json:"starts_at"`
	EndsAt         *string    `json:"ends_at"`
	AllDay         bool       `json:"all_day"`
	MultiDay       bool       `json:"multi_day"`
	RecurrenceRule *string    `json:"recurrence_rule"`
	RecurrenceText *string    `json:"recurrence_text"`
	Location       Location   `json:"location"`
	Published      bool       `json:"published"`
	ShowOnWebsite  bool       `json:"show_on_website"`
	RSVPEnabled    bool       `json:"rsvp_enabled"`
	RSVPCount      int        `json:"rsvp_count"`
	Tags           []EventTag `json:"tags"`
	Image          *Image     `json:"image"`
	CreatedBy      *User      `json:"created_by"`
	CreatedAt      string     `json:"created_at"`
	UpdatedAt      string     `json:"updated_at"`
	URL            string     `json:"url"`
}

// Location is where an event happens. The object is always present; every
// member may be null.
type Location struct {
	Name      *string  `json:"name"`
	Address   *string  `json:"address"`
	MapURL    *string  `json:"map_url"`
	Latitude  *float64 `json:"latitude"`
	Longitude *float64 `json:"longitude"`
}

// EventTag is a tag as an event lists it: no position, unlike Tag.
type EventTag struct {
	ID    string  `json:"id"`
	Name  string  `json:"name"`
	Color *string `json:"color"`
}

// Image is an event's image: an upload's URL or a linked image_url.
type Image struct {
	URL string  `json:"url"`
	Alt *string `json:"alt"`
}

// User is a person as other objects refer to one: an event's created_by and
// a CLI token's user.
type User struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Occurrence is one item of GET /events (events.json): the whole event plus
// the dates and times of this occurrence. A recurring event appears once per
// occurrence. The occurrence times are null for all-day events.
type Occurrence struct {
	Event
	OccurrenceDate     string  `json:"occurrence_date"`
	OccurrenceStartsAt *string `json:"occurrence_starts_at"`
	OccurrenceEndsAt   *string `json:"occurrence_ends_at"`
}

// Tag is a tag as /tags returns it (tag.json, tags.json). Color is
// "#RRGGBB", "" (the app allows a blank color: tag.rb's allow_blank), or
// null; position may be null.
type Tag struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Color    *string `json:"color"`
	Position *int    `json:"position"`
}

// RSVP is one response to an event (rsvps.json). OccurrenceDate says which
// occurrence of a recurring event it's for.
type RSVP struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Email          string  `json:"email"`
	Phone          *string `json:"phone"`
	OccurrenceDate *string `json:"occurrence_date"`
	CreatedAt      string  `json:"created_at"`
}

// Identity is GET /my/identity (identity.json): who the token belongs to,
// what it may do ("read" or "write"), and the calendar's time zone.
type Identity struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	EmailAddress string `json:"email_address"`
	Permission   string `json:"permission"`
	TimeZone     string `json:"time_zone"`
}

// AccessToken is one of your personal access tokens (access_tokens.json).
// The token itself is never listed.
type AccessToken struct {
	ID          string  `json:"id"`
	Description string  `json:"description"`
	Permission  string  `json:"permission"`
	LastUsedAt  *string `json:"last_used_at"`
	CreatedAt   string  `json:"created_at"`
}

// WebsiteToken is GET /api_token (api_token.json): whether the church
// website's token exists. Everything but Exists is null when it doesn't.
type WebsiteToken struct {
	Exists      bool    `json:"exists"`
	Description *string `json:"description"`
	LastUsedAt  *string `json:"last_used_at"`
	CreatedAt   *string `json:"created_at"`
}

// RotatedWebsiteToken is POST /api_token (api_token_rotated.json): the new
// website token, shown this once.
type RotatedWebsiteToken struct {
	Token     string `json:"token"`
	CreatedAt string `json:"created_at"`
}

// CLIToken is POST /cli/token (cli_token.json): the token a browser login
// minted, its permission, and whose it is.
type CLIToken struct {
	Token      string `json:"token"`
	Permission string `json:"permission"`
	User       User   `json:"user"`
}

// APIError is the body of most error responses, e.g. {"error":"not_found"}
// (the six error_*.json with an "error" key). Rails' own errors add a
// "status" member, which lenient decoding ignores. Show the string; never
// branch on it, except to word read_only_token.
type APIError struct {
	Error string `json:"error"`
}

// ValidationErrors is a 422 body, e.g. {"errors":{"title":["can't be
// blank"]}} (error_validation.json): messages per attribute, in the order
// the server validated them. A Go map loses that order, so the client reads
// it from the raw body to pick the first message.
type ValidationErrors struct {
	Errors map[string][]string `json:"errors"`
}
