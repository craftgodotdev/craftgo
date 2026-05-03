package lexer

import "fmt"

// Kind enumerates every token category emitted by the lexer.
//
// Kind values are stable and ordered: the keyword block (KwPackage..KwNull) and
// the HTTP-verb block (VerbGet..VerbOptions) are contiguous so that callers can
// detect "any keyword" via simple range checks. New kinds must be appended;
// reordering breaks parser code that relies on the keyword range.
type Kind int

const (
	// EOF is emitted exactly once at the end of input.
	EOF Kind = iota
	// Error wraps a malformed token; the offending source slice is in Text and
	// a Diagnostic is recorded on the [Lexer]. Parsing should treat this as
	// "skip and continue" - the diagnostic carries the message for users.
	Error

	// Ident is any identifier that is not a reserved keyword.
	Ident
	// Int holds a plain decimal integer literal (no sign, no suffix).
	Int
	// Float holds a decimal float literal of the form `digits.digits`.
	Float
	// String holds a double-quoted string with escape sequences preserved
	// verbatim (parser does the unescape).
	String
	// RawString holds a backtick-quoted string. Backticks are kept in Text;
	// no escape processing is performed.
	RawString
	// Duration is a numeric literal followed immediately by a duration suffix
	// (`ns`, `us`, `µs`, `ms`, `s`, `m`, `h`).
	Duration
	// Size is a numeric literal followed immediately by a size suffix
	// (`B`, `KB`, `MB`, `GB`).
	Size

	// --- Keywords (must stay contiguous; see [isKeywordKind] in parser). ---

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

	// --- HTTP verbs (also keyword-class). ---

	VerbGet
	VerbPost
	VerbPut
	VerbPatch
	VerbDelete
	VerbHead
	VerbOptions

	// --- Punctuation. ---

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

// kindNames maps each [Kind] to a human-readable label used by [Kind.String].
// Keep in sync with the const block above.
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

	VerbGet: "get", VerbPost: "post", VerbPut: "put", VerbPatch: "patch",
	VerbDelete: "delete", VerbHead: "head", VerbOptions: "options",

	LBrace: "{", RBrace: "}", LParen: "(", RParen: ")",
	LBracket: "[", RBracket: "]", LAngle: "<", RAngle: ">",
	Comma: ",", Colon: ":", Equal: "=", Question: "?",
	Dot: ".", Slash: "/", At: "@", Dash: "-",
}

// String returns a human-readable name for the kind, e.g. `EOF`, `Ident`, or
// the literal punctuation character. Unknown kinds (added without updating
// [kindNames]) render as `Kind(N)` so they remain visible in diagnostics.
func (k Kind) String() string {
	if s, ok := kindNames[k]; ok {
		return s
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

// keywords maps the spelling of every reserved word to its [Kind]. The lexer
// looks up identifiers here after collecting them, so any string that matches
// becomes the corresponding keyword token instead of an [Ident].
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

	"get":     VerbGet,
	"post":    VerbPost,
	"put":     VerbPut,
	"patch":   VerbPatch,
	"delete":  VerbDelete,
	"head":    VerbHead,
	"options": VerbOptions,
}

// CommentKind classifies a `//` comment by its source position relative
// to surrounding tokens. Used by the formatter to render leading vs
// trailing comments at the correct site after parser/AST has lost the
// raw column information.
type CommentKind uint8

const (
	// CommentLeading is the default - the comment was preceded only by
	// whitespace on its source line.
	CommentLeading CommentKind = iota
	// CommentTrailing is a comment that follows non-whitespace code on
	// the same line as the previously emitted token, e.g. the
	// `// 5 MiB` in `@max(5242880) // 5 MiB`. The lexer detects this
	// via [Lexer.sawNewlineSinceLastToken].
	CommentTrailing
)

// String returns "leading" or "trailing" for diagnostic messages.
func (k CommentKind) String() string {
	if k == CommentTrailing {
		return "trailing"
	}
	return "leading"
}

// Comment is one source-level `//` line, captured by the lexer. The
// formatter / future linters consume the full slice via
// [Lexer.Comments]; the parser snapshots it onto `*ast.File.Comments`
// so downstream tools see one canonical view of every comment in the
// file regardless of whether it ended up attached to an AST node.
type Comment struct {
	Pos  Position    // position of the leading `/` on the comment line
	Text string      // comment body with leading `// ` (and one optional space) stripped
	Kind CommentKind // leading vs trailing
}

// Token is a single lexed unit of the source.
//
// Text holds the literal source slice that produced this token (including
// surrounding quotes for [String] / [RawString], suffix for [Duration] /
// [Size]). For keyword tokens, Text is the keyword spelling - useful when
// echoing source without consulting [kindNames].
type Token struct {
	Kind Kind
	Text string
	Pos  Position
	// Doc is the contiguous run of `//` line comments immediately
	// preceding this token, with the leading `//` and a single trailing
	// space stripped. A blank line between a comment block and the next
	// token discards the block - only "doc-attached" comments arrive here.
	Doc []string
	// Trailing is a single `// note` comment that follows this token on
	// the same source line, with the leading `// ` stripped. Empty when
	// no trailing comment is present. Captured by [Lexer.Next] right
	// after the token is constructed; the comment text is consumed from
	// the source stream so [skipWhitespaceAndComments] on the next
	// [Lexer.Next] call does not see it again as a leading comment.
	Trailing string
}

// String formats the token for debug and test output as `Kind "text" at pos`.
func (t Token) String() string {
	return fmt.Sprintf("%s %q at %s", t.Kind, t.Text, t.Pos)
}
