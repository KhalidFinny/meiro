package youtube

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
)

type decipherOperation func([]byte) []byte

type playerDecipher struct {
	source []byte

	signatureOnce sync.Once
	signatureOps  []decipherOperation
	signatureErr  error

	nOnce     sync.Once
	nFunction string
	nErr      error

	nsigOnce   sync.Once
	nsigScript string
	nsigErr    error
}

const jsIdentifierPattern = `[A-Za-z_$][A-Za-z0-9_$]*`

var (
	playerMethodPattern            = regexp.MustCompile(`(` + jsIdentifierPattern + `)\s*:\s*function\(([^)]*)\)\{([^{}]*)\}`)
	playerSignatureFunctionPattern = regexp.MustCompile(`(?s)function(?:\s+` + jsIdentifierPattern + `)?\(a\)\{a\s*=\s*a\.split\(""\);(.*?)return\s+a\.join\(""\)\}`)
	playerOperationCallPattern     = regexp.MustCompile(`(?:a\s*=\s*)?(` + jsIdentifierPattern + `)\.(` + jsIdentifierPattern + `)\(a(?:\s*,\s*(\d+))?\)\s*;`)
	playerNFunctionPattern         = regexp.MustCompile(`\.get\(\s*["']n["']\s*\)\s*\)\s*&&\s*\(b\s*=\s*([A-Za-z0-9$]{0,3})\s*\[\s*(\d+)\s*\](.+?)\|\|\s*([A-Za-z0-9$]{1,8})`)
	playerTimeoutError             = errors.New("player decipher JavaScript timed out")
)

func newPlayerDecipher(source []byte) *playerDecipher {
	return &playerDecipher{source: source}
}

func (streaming *StreamingData) resolveAudioFormats(ctx context.Context, decoder *playerDecipher, clientVersion, poToken, cpn string) {
	nCache := make(map[string]string)
	for _, formats := range [][]AudioFormat{streaming.Formats, streaming.AdaptiveFormats} {
		for index := range formats {
			formats[index].ResolvedURL = ""
			formats[index].DecipherError = ""
			if formats[index].URL == "" && formats[index].SignatureCipher == "" && formats[index].Cipher == "" {
				continue
			}
			resolved, err := decoder.resolveFormatURLForClientWithCacheAndCPN(ctx, formats[index], clientVersion, poToken, cpn, nCache)
			if err != nil {
				formats[index].DecipherError = err.Error()
				continue
			}
			formats[index].ResolvedURL = resolved
		}
	}
}

func (decoder *playerDecipher) resolveFormatURL(ctx context.Context, format AudioFormat) (string, error) {
	return decoder.resolveFormatURLForClient(ctx, format, "", "")
}

func (decoder *playerDecipher) resolveFormatURLForClient(ctx context.Context, format AudioFormat, clientVersion, poToken string) (string, error) {
	return decoder.resolveFormatURLForClientWithCacheAndCPN(ctx, format, clientVersion, poToken, "", make(map[string]string))
}

func (decoder *playerDecipher) resolveFormatURLForClientWithCacheAndCPN(ctx context.Context, format AudioFormat, clientVersion, poToken, cpn string, nCache map[string]string) (string, error) {
	streamURL := format.URL
	var signatureCipher url.Values
	cipherText := format.SignatureCipher
	if cipherText == "" {
		cipherText = format.Cipher
	}
	if cipherText != "" {
		parsed, err := url.ParseQuery(cipherText)
		if err != nil {
			return "", fmt.Errorf("parse signature cipher: %w", err)
		}
		signatureCipher = parsed
		if cipherURL := parsed.Get("url"); cipherURL != "" {
			streamURL = cipherURL
		}
	}
	if streamURL == "" {
		return "", errors.New("format has no stream URL")
	}
	parsedURL, err := url.Parse(streamURL)
	if err != nil {
		return "", fmt.Errorf("parse stream URL: %w", err)
	}
	query := parsedURL.Query()
	var encryptedSignature string
	signatureKey := "signature"
	if signatureCipher != nil {
		signatureKey = signatureCipher.Get("sp")
		if signatureKey == "" {
			signatureKey = "signature"
		}
		encryptedSignature = signatureCipher.Get("s")
		if encryptedSignature == "" {
			if signature := signatureCipher.Get("sig"); signature != "" {
				query.Set(signatureKey, signature)
			}
		}
	}

	encryptedN := query.Get("n")
	transformedN := false
	transformedSignature := false
	var nsigErr error
	if encryptedN != "" || encryptedSignature != "" {
		if encryptedN != "" {
			if cached, ok := nCache[encryptedN]; ok {
				query.Set("n", cached)
				transformedN = true
			}
		}
		if !transformedN || encryptedSignature != "" {
			if script, err := decoder.getNSigScript(); err != nil {
				nsigErr = err
			} else {
				input := cloneURLValues(query)
				if transformedN {
					input.Del("n")
				}
				output, err := evaluatePlayerNSig(ctx, script, input, signatureKey, encryptedSignature)
				if err != nil {
					nsigErr = err
				} else {
					query = output
					if encryptedN != "" {
						if cached, ok := nCache[encryptedN]; ok {
							query.Set("n", cached)
							transformedN = true
						} else if transformed := query.Get("n"); transformed != "" {
							query.Set("n", transformed)
							nCache[encryptedN] = transformed
							transformedN = true
						}
					}
					transformedSignature = encryptedSignature != ""
				}
			}
		}
	}
	if encryptedSignature != "" && !transformedSignature {
		operations, err := decoder.getSignatureOperations()
		if err != nil {
			if nsigErr != nil {
				return "", fmt.Errorf("decipher signature: %w (combined transform: %v)", err, nsigErr)
			}
			return "", err
		}
		decoded := []byte(encryptedSignature)
		for _, operation := range operations {
			decoded = operation(decoded)
		}
		query.Set(signatureKey, string(decoded))
	}
	if encryptedN != "" && !transformedN {
		function, err := decoder.getNFunction()
		if err != nil {
			if nsigErr != nil {
				return "", fmt.Errorf("decipher n parameter: %w (combined transform: %v)", err, nsigErr)
			}
			return "", err
		}
		decrypted, err := evaluatePlayerFunction(ctx, function, encryptedN)
		if err != nil {
			return "", fmt.Errorf("decipher n parameter: %w", err)
		}
		query.Set("n", decrypted)
		nCache[encryptedN] = decrypted
	}
	if clientVersion != "" {
		query.Set("cver", clientVersion)
	}
	if poToken != "" && query.Get("sabr") != "1" {
		query.Set("pot", poToken)
	}
	if cpn != "" {
		query.Set("cpn", cpn)
	}
	parsedURL.RawQuery = query.Encode()
	return parsedURL.String(), nil
}

func generateCPN() (string, error) {
	value := make([]byte, 12)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("youtube: generate playback client ID: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

// OpenAudioStream requests an audio format's resolved URL and returns the
// streaming response. The caller must close the response body.
func (c *Client) OpenAudioStream(ctx context.Context, format AudioFormat) (*http.Response, error) {
	if !strings.HasPrefix(format.MimeType, "audio/") {
		return nil, errors.New("youtube: format is not audio")
	}
	streamURL := format.PlayableURL()
	if streamURL == "" {
		return nil, errors.New("youtube: audio format has no playable URL")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, streamURL, nil)
	if err != nil {
		return nil, fmt.Errorf("youtube: create audio stream request: %w", err)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("youtube: open audio stream: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		defer response.Body.Close()
		return nil, responseError("open audio stream", response)
	}
	return response, nil
}

func cloneURLValues(values url.Values) url.Values {
	cloned := make(url.Values, len(values))
	for key, entries := range values {
		cloned[key] = append([]string(nil), entries...)
	}
	return cloned
}

func (decoder *playerDecipher) getNSigScript() (string, error) {
	decoder.nsigOnce.Do(func() {
		decoder.nsigScript, decoder.nsigErr = extractNSigScript(decoder.source)
	})
	if decoder.nsigErr != nil {
		return "", decoder.nsigErr
	}
	return decoder.nsigScript, nil
}

func (decoder *playerDecipher) getSignatureOperations() ([]decipherOperation, error) {
	decoder.signatureOnce.Do(func() {
		decoder.signatureOps, decoder.signatureErr = parseSignatureOperations(decoder.source)
	})
	if decoder.signatureErr != nil {
		return nil, decoder.signatureErr
	}
	return decoder.signatureOps, nil
}

func parseSignatureOperations(source []byte) ([]decipherOperation, error) {
	functions := playerSignatureFunctionPattern.FindAllSubmatch(source, -1)
	for _, function := range functions {
		calls := playerOperationCallPattern.FindAllSubmatch(function[1], -1)
		if len(calls) == 0 {
			continue
		}
		objectName := string(calls[0][1])
		objectBody, err := playerObjectBody(source, objectName)
		if err != nil {
			continue
		}
		actions := playerObjectActions(objectBody)
		var operations []decipherOperation
		valid := true
		for _, call := range calls {
			if string(call[1]) != objectName {
				valid = false
				break
			}
			action, exists := actions[string(call[2])]
			if !exists {
				valid = false
				break
			}
			argument, _ := strconv.Atoi(string(call[3]))
			operations = append(operations, action.operation(argument))
		}
		if valid && len(operations) > 0 {
			return operations, nil
		}
	}
	return nil, errors.New("player signature transform is not supported")
}

type playerAction struct {
	kind string
}

func (action playerAction) operation(argument int) decipherOperation {
	switch action.kind {
	case "reverse":
		return func(value []byte) []byte {
			for left, right := 0, len(value)-1; left < right; left, right = left+1, right-1 {
				value[left], value[right] = value[right], value[left]
			}
			return value
		}
	case "splice":
		return func(value []byte) []byte {
			position := argument
			if position > len(value) {
				position = len(value)
			}
			return value[position:]
		}
	case "swap":
		return func(value []byte) []byte {
			if len(value) == 0 {
				return value
			}
			position := argument % len(value)
			value[0], value[position] = value[position], value[0]
			return value
		}
	default:
		return func(value []byte) []byte { return value }
	}
}

func playerObjectActions(body []byte) map[string]playerAction {
	actions := make(map[string]playerAction)
	for _, method := range playerMethodPattern.FindAllSubmatch(body, -1) {
		parameters := strings.Split(string(method[2]), ",")
		if len(parameters) < 1 || strings.TrimSpace(parameters[0]) != "a" {
			continue
		}
		methodBody := compactJavaScript(string(method[3]))
		kind := ""
		switch {
		case strings.Contains(methodBody, "a.reverse()"):
			kind = "reverse"
		case strings.Contains(methodBody, "a.splice(0,b)"):
			kind = "splice"
		case strings.Contains(methodBody, "a[0]=a[b%a.length]") && strings.Contains(methodBody, "a[b%a.length]=c"):
			kind = "swap"
		case strings.Contains(methodBody, "a[0]=a[b]") && strings.Contains(methodBody, "a[b]=c"):
			kind = "swap"
		}
		if kind != "" {
			actions[string(method[1])] = playerAction{kind: kind}
		}
	}
	return actions
}

func compactJavaScript(source string) string {
	return strings.Map(func(character rune) rune {
		if character == ' ' || character == '\n' || character == '\r' || character == '\t' {
			return -1
		}
		return character
	}, source)
}

func playerObjectBody(source []byte, name string) ([]byte, error) {
	declaration := regexp.MustCompile(`(?:^|[;,{])\s*(?:var\s+)?` + regexp.QuoteMeta(name) + `\s*=\s*\{`)
	match := declaration.FindIndex(source)
	if match == nil {
		return nil, fmt.Errorf("player signature action object %q not found", name)
	}
	open := bytes.IndexByte(source[match[0]:match[1]], '{') + match[0]
	close, err := matchingJavaScriptBrace(source, open)
	if err != nil {
		return nil, err
	}
	return source[open+1 : close], nil
}

func matchingJavaScriptBrace(source []byte, open int) (int, error) {
	depth := 0
	var quote byte
	escaped := false
	lineComment := false
	blockComment := false
	for index := open; index < len(source); index++ {
		character := source[index]
		if lineComment {
			if character == '\n' {
				lineComment = false
			}
			continue
		}
		if blockComment {
			if character == '*' && index+1 < len(source) && source[index+1] == '/' {
				blockComment = false
				index++
			}
			continue
		}
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if character == '\\' {
				escaped = true
				continue
			}
			if character == quote {
				quote = 0
			}
			continue
		}
		if character == '/' && index+1 < len(source) {
			switch source[index+1] {
			case '/':
				lineComment = true
				index++
				continue
			case '*':
				blockComment = true
				index++
				continue
			}
		}
		switch character {
		case '\'', '"', '`':
			quote = character
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return index, nil
			}
		}
	}
	return 0, errors.New("unterminated JavaScript object or function")
}

func (decoder *playerDecipher) getNFunction() (string, error) {
	decoder.nOnce.Do(func() {
		decoder.nFunction, decoder.nErr = extractNFunction(decoder.source)
	})
	if decoder.nErr != nil {
		return "", decoder.nErr
	}
	return decoder.nFunction, nil
}

func extractNFunction(source []byte) (string, error) {
	match := playerNFunctionPattern.FindSubmatch(source)
	if len(match) < 5 {
		return "", errors.New("player n transform function is not supported")
	}
	index, err := strconv.Atoi(string(match[2]))
	if err != nil {
		return "", fmt.Errorf("parse player n transform index: %w", err)
	}
	name := string(match[1])
	if index == 0 {
		name = string(match[4])
	}
	if name == "" {
		return "", errors.New("player n transform function name is empty")
	}
	return extractNamedFunction(source, name)
}

func extractNamedFunction(source []byte, name string) (string, error) {
	declaration := regexp.MustCompile(`(?:^|[;,{])\s*(?:var\s+)?` + regexp.QuoteMeta(name) + `\s*=\s*function\s*\(`)
	match := declaration.FindIndex(source)
	if match == nil {
		return "", fmt.Errorf("player n transform function %q not found", name)
	}
	start := bytes.Index(source[match[0]:match[1]], []byte(name)) + match[0]
	open := bytes.IndexByte(source[match[0]:], '{') + match[0]
	close, err := matchingJavaScriptBrace(source, open)
	if err != nil {
		return "", err
	}
	return string(source[start : close+1]), nil
}

func evaluatePlayerFunction(ctx context.Context, function, argument string) (string, error) {
	equals := strings.IndexByte(function, '=')
	if equals < 0 {
		return "", errors.New("invalid player n transform function")
	}
	functionExpression := strings.TrimSpace(function[equals+1:])
	vm := goja.New()
	stopContextInterrupt := context.AfterFunc(ctx, func() { vm.Interrupt(ctx.Err()) })
	timer := time.AfterFunc(3*time.Second, func() { vm.Interrupt(playerTimeoutError) })
	defer stopContextInterrupt()
	defer timer.Stop()
	value, err := vm.RunString("var __youtubeN = (" + functionExpression + "); __youtubeN")
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("compile player n transform: %w", err)
	}
	functionValue, ok := goja.AssertFunction(value)
	if !ok {
		return "", errors.New("player n transform is not callable")
	}
	result, err := functionValue(goja.Undefined(), vm.ToValue(argument))
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if errors.Is(err, playerTimeoutError) {
			return "", err
		}
		return "", fmt.Errorf("run player n transform: %w", err)
	}
	if result == nil || goja.IsUndefined(result) || goja.IsNull(result) {
		return "", errors.New("player n transform returned no value")
	}
	return result.String(), nil
}
