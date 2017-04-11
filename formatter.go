package prefixed

import (
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/mgutz/ansi"
)

const reset = ansi.Reset

var (
	baseTimestamp time.Time
	defaultColorScheme *ColorScheme = &ColorScheme{
		InfoLevelStyle: "green",
		WarnLevelStyle: "yellow",
		ErrorLevelStyle: "red",
		FatalLevelStyle: "red",
		PanicLevelStyle: "red",
		DebugLevelStyle: "blue",
		PrefixStyle: "cyan",
		TimestampStyle: "black+h",
	}
)

func init() {
	baseTimestamp = time.Now()
	defaultColorScheme = compileColorScheme(defaultColorScheme)
}

func miniTS() int {
	return int(time.Since(baseTimestamp) / time.Second)
}

type ColorScheme struct {
	InfoLevelStyle string
	WarnLevelStyle string
	ErrorLevelStyle string
	FatalLevelStyle string
	PanicLevelStyle string
	DebugLevelStyle string
	PrefixStyle string
	TimestampStyle string
}

type compiledColorScheme struct {
	InfoLevelColor func(string) string
	WarnLevelColor func(string) string
	ErrorLevelColor func(string) string
	FatalLevelColor func(string) string
	PanicLevelColor func(string) string
	DebugLevelColor func(string) string
	PrefixColor func(string) string
	TimestampColor func(string) string
}

type TextFormatter struct {
	// Set to true to bypass checking for a TTY before outputting colors.
	ForceColors bool

	// Force disabling colors.
	DisableColors bool

	// Disable timestamp logging. useful when output is redirected to logging
	// system that already adds timestamps.
	DisableTimestamp bool

	// Enable logging the full timestamp when a TTY is attached instead of just
	// the time passed since beginning of execution.
	FullTimestamp bool

	// Timestamp format to use for display when a full timestamp is printed.
	TimestampFormat string

	// The fields are sorted by default for a consistent output. For applications
	// that log extremely frequently and don't use the JSON formatter this may not
	// be desired.
	DisableSorting bool

	// Wrap empty fields in quotes if true.
	QuoteEmptyFields bool

	// Can be set to the override the default quoting character "
	// with something else. For example: ', or `.
	QuoteCharacter string

	// Pad msg field with spaces on the right for display.
	// The value for this parameter will be the size of padding.
	// Its default value is zero, which means no padding will be applied for msg.
	SpacePadding int

	// Color scheme to use.
	colorScheme *compiledColorScheme

	// Whether the logger's out is to a terminal
	isTerminal bool

	sync.Once
}

func compileColorScheme(s *ColorScheme) *compiledColorScheme {
	return &compiledColorScheme{
		InfoLevelColor: ansi.ColorFunc(s.InfoLevelStyle),
		WarnLevelColor: ansi.ColorFunc(s.WarnLevelStyle),
		ErrorLevelColor: ansi.ColorFunc(s.ErrorLevelStyle),
		FatalLevelColor: ansi.ColorFunc(s.FatalLevelStyle),
		PanicLevelColor: ansi.ColorFunc(s.PanicLevelStyle),
		DebugLevelColor: ansi.ColorFunc(s.DebugLevelStyle),
		PrefixColor: ansi.ColorFunc(s.PrefixStyle),
		TimestampColor: ansi.ColorFunc(s.TimestampStyle),
	}
}

func (f *TextFormatter) init(entry *logrus.Entry) {
	if len(f.QuoteCharacter) == 0 {
		f.QuoteCharacter = "\""
	}
	if entry.Logger != nil {
		f.isTerminal = logrus.IsTerminal(entry.Logger.Out)
	}
}

func (f *TextFormatter) SetColorScheme(colorScheme *ColorScheme) {
	f.colorScheme = compileColorScheme(colorScheme)
}

func (f *TextFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	var b *bytes.Buffer
	var keys []string = make([]string, 0, len(entry.Data))
	for k := range entry.Data {
		keys = append(keys, k)
	}

	if !f.DisableSorting {
		sort.Strings(keys)
	}
	if entry.Buffer != nil {
		b = entry.Buffer
	} else {
		b = &bytes.Buffer{}
	}

	prefixFieldClashes(entry.Data)

	f.Do(func() { f.init(entry) })

	isColored := (f.ForceColors || f.isTerminal) && !f.DisableColors

	timestampFormat := f.TimestampFormat
	if timestampFormat == "" {
		timestampFormat = logrus.DefaultTimestampFormat
	}
	if isColored {
		f.printColored(b, entry, keys, timestampFormat)
	} else {
		if !f.DisableTimestamp {
			f.appendKeyValue(b, "time", entry.Time.Format(timestampFormat))
		}
		f.appendKeyValue(b, "level", entry.Level.String())
		if entry.Message != "" {
			f.appendKeyValue(b, "msg", entry.Message)
		}
		for _, key := range keys {
			f.appendKeyValue(b, key, entry.Data[key])
		}
	}

	b.WriteByte('\n')
	return b.Bytes(), nil
}

func (f *TextFormatter) printColored(b *bytes.Buffer, entry *logrus.Entry, keys []string, timestampFormat string) {
	var levelColor func(string) string
	var levelText string
	switch entry.Level {
	case logrus.InfoLevel:
		levelColor = f.colorScheme.InfoLevelColor
	case logrus.WarnLevel:
		levelColor = f.colorScheme.WarnLevelColor
	case logrus.ErrorLevel:
		levelColor = f.colorScheme.ErrorLevelColor
	case logrus.FatalLevel:
		levelColor = f.colorScheme.FatalLevelColor
	case logrus.PanicLevel:
		levelColor = f.colorScheme.PanicLevelColor
	default:
		levelColor = f.colorScheme.DebugLevelColor
	}

	if entry.Level != logrus.WarnLevel {
		levelText = strings.ToUpper(entry.Level.String())
	} else {
		levelText = "WARN"
	}

	prefix := ""
	message := entry.Message

	if prefixValue, ok := entry.Data["prefix"]; ok {
		prefix = fmt.Sprint(" ", f.colorScheme.PrefixColor(prefixValue+":"))
	} else {
		prefixValue, trimmedMsg := extractPrefix(entry.Message)
		if len(prefixValue) > 0 {
			prefix = fmt.Sprint(" ", f.colorScheme.PrefixColor(prefixValue+":"))
			message = trimmedMsg
		}
	}

	messageFormat := "%s"
	if f.SpacePadding != 0 {
		messageFormat = fmt.Sprintf("%%-%ds", f.SpacePadding)
	}

	if f.DisableTimestamp {
		fmt.Fprintf(b, "%+5s%s "+messageFormat, levelColor(levelText), prefix, message)
	} else {
		if f.ShortTimestamp {
			fmt.Fprintf(b, "%s[%04d]%s %s%+5s%s%s "+messageFormat, ansi.LightBlack, miniTS(), reset, levelColor, levelText, reset, prefix, message)
		} else {
			fmt.Fprintf(b, "%s[%s]%s %s%+5s%s%s "+messageFormat, ansi.LightBlack, entry.Time.Format(timestampFormat), reset, levelColor, levelText, reset, prefix, message)
		}
	}
	for _, k := range keys {
		if (k != "prefix") {
			v := entry.Data[k]
			fmt.Fprintf(b, " %s%s%s=%+v", levelColor, k, reset, v)
		}
	}
}

func (f *TextFormatter) needsQuoting(text string) bool {
	if f.QuoteEmptyFields && len(text) == 0 {
		return true
	}
	for _, ch := range text {
		if !((ch >= 'a' && ch <= 'z') ||
			(ch >= 'A' && ch <= 'Z') ||
			(ch >= '0' && ch <= '9') ||
			ch == '-' || ch == '.') {
			return true
		}
	}
	return false
}

func extractPrefix(msg string) (string, string) {
	prefix := ""
	regex := regexp.MustCompile("^\\[(.*?)\\]")
	if regex.MatchString(msg) {
		match := regex.FindString(msg)
		prefix, msg = match[1:len(match)-1], strings.TrimSpace(msg[len(match):])
	}
	return prefix, msg
}

func (f *TextFormatter) appendKeyValue(b *bytes.Buffer, key string, value interface{}) {
	b.WriteString(key)
	b.WriteByte('=')

	switch value := value.(type) {
	case string:
		if needsQuoting(value) {
			b.WriteString(value)
		} else {
			fmt.Fprintf(b, "%q", value)
		}
	case error:
		errmsg := value.Error()
		if needsQuoting(errmsg) {
			b.WriteString(errmsg)
		} else {
			fmt.Fprintf(b, "%q", value)
		}
	default:
		fmt.Fprint(b, value)
	}

	b.WriteByte(' ')
}

// This is to not silently overwrite `time`, `msg` and `level` fields when
// dumping it. If this code wasn't there doing:
//
//  logrus.WithField("level", 1).Info("hello")
//
// would just silently drop the user provided level. Instead with this code
// it'll be logged as:
//
//  {"level": "info", "fields.level": 1, "msg": "hello", "time": "..."}
func prefixFieldClashes(data logrus.Fields) {
	if t, ok := data["time"]; ok {
		data["fields.time"] = t
	}

	if m, ok := data["msg"]; ok {
		data["fields.msg"] = m
	}

	if l, ok := data["level"]; ok {
		data["fields.level"] = l
	}
}
