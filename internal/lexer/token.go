package lexer

import "fmt"

// Kind is a token category. The reserved words, KwPackage through VerbOptions,
// are contiguous so callers can test for one with a range check.
type Kind int

const (
	// EOF marks the end of input.
	EOF Kind = iota
	// Error is a malformed token; Text holds the diagnostic message.
	Error

	// Ident is an identifier that is not a reserved word.
	Ident
	// Int is an unsigned decimal integer without a suffix.
	Int
	// Float is `digits.digits`.
	Float
	// String is a double-quoted literal; Text keeps the quotes and escapes.
	String
	// RawString is a backtick literal; Text keeps the backticks.
	RawString
	// Duration is a number with a [DurationUnits] suffix.
	Duration
	// Size is a number with a [SizeUnits] suffix.
	Size

	// Keywords.

	KwPackage
	KwImport
	KwType
	KwEnum
	KwError
	KwScalar
	KwService
	KwExtend
	KwMiddleware
	KwRequest
	KwResponse
	KwMap
	KwTrue
	KwFalse
	KwNull
	KwEvent
	KwPayload

	// HTTP verbs, also reserved words.

	VerbGet
	VerbPost
	VerbPut
	VerbPatch
	VerbDelete
	VerbHead
	VerbOptions

	// Punctuation.

	LBrace   // {
	RBrace   // }
	LParen   // (
	RParen   // )
	LBracket // [
	RBracket // ]
	LAngle   // <
	RAngle   // >
	Comma    // ,
	Colon    // :
	Equal    // =
	Question // ?
	Dot      // .
	Slash    // /
	At       // @
	Dash     // -
)

var kindNames = map[Kind]string{
	EOF: "EOF", Error: "Error",
	Ident: "Ident", Int: "Int", Float: "Float",
	String: "String", RawString: "RawString",
	Duration: "Duration", Size: "Size",

	KwPackage: "package", KwImport: "import", KwType: "type",
	KwEnum: "enum", KwError: "error", KwScalar: "scalar",
	KwService: "service", KwExtend: "extend", KwMiddleware: "middleware",
	KwRequest: "request", KwResponse: "response",
	KwMap: "map", KwTrue: "true", KwFalse: "false", KwNull: "null",
	KwEvent: "event", KwPayload: "payload",

	VerbGet: "get", VerbPost: "post", VerbPut: "put", VerbPatch: "patch",
	VerbDelete: "delete", VerbHead: "head", VerbOptions: "options",

	LBrace: "{", RBrace: "}", LParen: "(", RParen: ")",
	LBracket: "[", RBracket: "]", LAngle: "<", RAngle: ">",
	Comma: ",", Colon: ":", Equal: "=", Question: "?",
	Dot: ".", Slash: "/", At: "@", Dash: "-",
}

// String returns the kind's spelling, or `Kind(N)` for an unknown kind.
func (k Kind) String() string {
	if s, ok := kindNames[k]; ok {
		return s
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

// keywords maps each reserved word to its Kind.
var keywords = map[string]Kind{
	"package":    KwPackage,
	"import":     KwImport,
	"type":       KwType,
	"enum":       KwEnum,
	"error":      KwError,
	"scalar":     KwScalar,
	"service":    KwService,
	"extend":     KwExtend,
	"middleware": KwMiddleware,
	"request":    KwRequest,
	"response":   KwResponse,
	"map":        KwMap,
	"true":       KwTrue,
	"false":      KwFalse,
	"null":       KwNull,
	"event":      KwEvent,
	"payload":    KwPayload,

	"get":     VerbGet,
	"post":    VerbPost,
	"put":     VerbPut,
	"patch":   VerbPatch,
	"delete":  VerbDelete,
	"head":    VerbHead,
	"options": VerbOptions,
}

// CommentKind says whether a comment follows code on its line.
type CommentKind uint8

const (
	// CommentLeading is a comment with no token before it on its line.
	CommentLeading CommentKind = iota
	// CommentTrailing is a comment after a token on the same line, as in
	// `@maxBodySize(5242880) // 5 MiB`.
	CommentTrailing
)

// String returns "leading" or "trailing".
func (k CommentKind) String() string {
	if k == CommentTrailing {
		return "trailing"
	}
	return "leading"
}

// Comment is one `//` comment.
type Comment struct {
	Pos  Position    // the first '/'
	Text string      // without the `//` and one following space
	Kind CommentKind // leading or trailing
}

// Token is one lexed token. Text is its source spelling, or the message for an
// [Error] token.
type Token struct {
	Kind Kind
	Text string
	Pos  Position
	// Doc holds the comment lines directly above the token.
	Doc []string
	// Trailing is the comment after the token on its line, if any.
	Trailing string
}

// String renders the token as `Kind "text" at pos`.
func (t Token) String() string {
	return fmt.Sprintf("%s %q at %s", t.Kind, t.Text, t.Pos)
}
