// Package discord implements the minimal slice of the Discord API that a
// scale-to-zero bot needs: HTTP-interaction request/response types, Ed25519
// request verification, and a small REST client for posting messages and
// registering slash commands.
//
// There is deliberately no Gateway (persistent WebSocket) code. A Gateway
// connection would require an always-on instance, which is incompatible with
// the "idle cost = 0" requirement. Everything here works over plain HTTP.
package discord

// Interaction request types (Discord "type" field on inbound payloads).
const (
	InteractionPing             = 1
	InteractionCommand          = 2
	InteractionMessageComponent = 3
	InteractionModalSubmit      = 5
)

// Interaction response types (Discord "type" field on our replies).
const (
	ResponsePong    = 1 // ack a PING
	ResponseMessage = 4 // CHANNEL_MESSAGE_WITH_SOURCE
	ResponseModal   = 9 // open a modal
)

// Component types.
const (
	ComponentActionRow = 1
	ComponentButton    = 2
	ComponentTextInput = 4
)

// ButtonStylePrimary is the blurple call-to-action style.
const ButtonStylePrimary = 1

// TextInputShort is a single-line modal input.
const TextInputShort = 1

// FlagEphemeral marks a response visible only to the invoking user.
const FlagEphemeral = 1 << 6

// Application command option types.
const OptionTypeString = 3

// Interaction is an inbound Discord interaction payload.
type Interaction struct {
	ID            string           `json:"id"`
	Type          int              `json:"type"`
	Token         string           `json:"token"`
	ApplicationID string           `json:"application_id"`
	ChannelID     string           `json:"channel_id"`
	GuildID       string           `json:"guild_id"`
	Data          *InteractionData `json:"data"`
	Member        *Member          `json:"member"`
	User          *User            `json:"user"`
}

// InteractionData carries command / component / modal payload details.
type InteractionData struct {
	Name       string          `json:"name"`      // slash command name
	CustomID   string          `json:"custom_id"` // component / modal id
	Options    []CommandOption `json:"options"`
	Components []ActionRow     `json:"components"` // present on modal submit
}

// CommandOption is one argument supplied to a slash command.
type CommandOption struct {
	Name  string `json:"name"`
	Type  int    `json:"type"`
	Value any    `json:"value"`
}

// Member wraps the guild member object (User nested inside).
type Member struct {
	User *User `json:"user"`
}

// User is a Discord user.
type User struct {
	ID         string `json:"id"`
	Username   string `json:"username"`
	GlobalName string `json:"global_name"`
}

// ActorID returns the invoking user's id whether the interaction came from a
// guild (Member.User) or a DM (User).
func (i *Interaction) ActorID() string {
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User.ID
	}
	if i.User != nil {
		return i.User.ID
	}
	return ""
}

// ActorName returns a human-friendly display name for the invoking user.
func (i *Interaction) ActorName() string {
	u := i.User
	if i.Member != nil && i.Member.User != nil {
		u = i.Member.User
	}
	if u == nil {
		return "anonymous"
	}
	if u.GlobalName != "" {
		return u.GlobalName
	}
	return u.Username
}

// OptString returns a string option value by name (empty if absent).
func (d *InteractionData) OptString(name string) string {
	for _, o := range d.Options {
		if o.Name == name {
			if s, ok := o.Value.(string); ok {
				return s
			}
		}
	}
	return ""
}

// ModalValue returns the first text-input value submitted in a modal.
func (d *InteractionData) ModalValue() string {
	for _, row := range d.Components {
		for _, c := range row.Components {
			if c.Value != "" {
				return c.Value
			}
		}
	}
	return ""
}

// ActionRow is a layout container for buttons and inputs.
type ActionRow struct {
	Type       int         `json:"type"`
	Components []Component `json:"components"`
}

// Component is a button, text input, etc. Fields are shared across kinds; only
// the relevant ones are populated and JSON-omitted when empty.
type Component struct {
	Type        int    `json:"type"`
	CustomID    string `json:"custom_id,omitempty"`
	Style       int    `json:"style,omitempty"`
	Label       string `json:"label,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
	MinLength   int    `json:"min_length,omitempty"`
	MaxLength   int    `json:"max_length,omitempty"`
	Required    bool   `json:"required,omitempty"`
	Value       string `json:"value,omitempty"` // populated on modal submit
}

// Response is our reply to an interaction webhook call.
type Response struct {
	Type int           `json:"type"`
	Data *ResponseData `json:"data,omitempty"`
}

// ResponseData is the body of a message or modal response.
type ResponseData struct {
	Content    string      `json:"content,omitempty"`
	Flags      int         `json:"flags,omitempty"`
	Embeds     []Embed     `json:"embeds,omitempty"`
	Components []ActionRow `json:"components,omitempty"`
	CustomID   string      `json:"custom_id,omitempty"` // modal id
	Title      string      `json:"title,omitempty"`     // modal title
}

// Embed is a rich message embed.
type Embed struct {
	Title       string       `json:"title,omitempty"`
	Description string       `json:"description,omitempty"`
	Color       int          `json:"color,omitempty"`
	Fields      []EmbedField `json:"fields,omitempty"`
	Footer      *EmbedFooter `json:"footer,omitempty"`
}

// EmbedField is a name/value pair inside an embed.
type EmbedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

// EmbedFooter is the small text line at the bottom of an embed.
type EmbedFooter struct {
	Text string `json:"text"`
}
