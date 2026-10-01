## The language server's JSON value (port of HTNLspJson). Integers (numbers
## without fraction or exponent that fit in int64) are kept apart from other
## numbers; objects keep the first occurrence of a duplicated key and
## serialize their keys in byte order, like the original std::map.

import std/[algorithm, tables]

type
  JsonKind* = enum
    jkNull, jkBool, jkInteger, jkNumber, jkString, jkArray, jkObject

  Json* = object
    case kind*: JsonKind
    of jkNull: discard
    of jkBool: boolValue: bool
    of jkInteger: integerValue: int64
    of jkNumber: numberValue: float64
    of jkString: stringValue: string
    of jkArray: arrayValue: seq[Json]
    of jkObject: objectValue: Table[string, Json]

  JsonParser = object
    text: string
    offset: int
    error: string

proc jsonNull*(): Json = Json(kind: jkNull)
proc jsonBool*(value: bool): Json = Json(kind: jkBool, boolValue: value)
proc jsonInteger*(value: int64): Json = Json(kind: jkInteger, integerValue: value)
proc jsonString*(value: string): Json = Json(kind: jkString, stringValue: value)
proc jsonArray*(values: varargs[Json]): Json = Json(kind: jkArray, arrayValue: @values)

proc jsonObject*(fields: varargs[(string, Json)]): Json =
  result = Json(kind: jkObject, objectValue: initTable[string, Json]())
  for (key, value) in fields:
    if key notin result.objectValue: result.objectValue[key] = value

proc isString*(j: Json): bool = j.kind == jkString
proc isInteger*(j: Json): bool = j.kind == jkInteger
proc isArray*(j: Json): bool = j.kind == jkArray

proc asString*(j: Json): string =
  if j.kind == jkString: j.stringValue else: ""

proc asInteger*(j: Json): int64 =
  if j.kind == jkInteger: j.integerValue else: 0

proc asArray*(j: Json): seq[Json] =
  if j.kind == jkArray: j.arrayValue else: @[]

proc find*(j: Json, key: string, value: var Json): bool =
  ## The member named `key` of an object.
  if j.kind != jkObject or key notin j.objectValue: return false
  value = j.objectValue[key]
  true

proc findNested*(j: Json, keys: openArray[string], value: var Json): bool =
  ## Follows a chain of object members.
  var current = j
  for key in keys:
    var next: Json
    if not current.find(key, next): return false
    current = next
  value = current
  true

proc set*(j: var Json, key: string, value: Json) =
  ## Adds or replaces an object member.
  if j.kind != jkObject: j = jsonObject()
  j.objectValue[key] = value

proc add*(j: var Json, value: Json) =
  ## Appends an array element.
  if j.kind != jkArray: j = jsonArray()
  j.arrayValue.add value

proc fail(p: var JsonParser, message: string): (Json, bool) =
  p.error = message
  (jsonNull(), false)

proc isSpace(c: char): bool {.inline.} =
  ## std::isspace in the "C" locale.
  c == ' ' or c in {'\t' .. '\r'}

proc skipWhitespace(p: var JsonParser) =
  while p.offset < p.text.len and isSpace(p.text[p.offset]): inc p.offset

proc consume(p: var JsonParser, c: char): bool =
  if p.offset < p.text.len and p.text[p.offset] == c:
    inc p.offset
    return true
  false

proc consumeLiteral(p: var JsonParser, literal: string): bool =
  if p.text.len - p.offset < literal.len: return false
  for i, c in literal:
    if p.text[p.offset + i] != c: return false
  p.offset += literal.len
  true

proc hexValue(c: char): int =
  case c
  of '0' .. '9': ord(c) - ord('0')
  of 'a' .. 'f': ord(c) - ord('a') + 10
  of 'A' .. 'F': ord(c) - ord('A') + 10
  else: -1

proc parseString(p: var JsonParser, output: var string): bool =
  p.skipWhitespace()
  if not p.consume('"'):
    p.error = "Expected JSON string"
    return false
  output = ""
  while p.offset < p.text.len:
    let c = p.text[p.offset]
    inc p.offset
    if c == '"': return true
    if c != '\\':
      output.add c
      continue
    if p.offset >= p.text.len:
      p.error = "Invalid JSON escape"
      return false
    let escape = p.text[p.offset]
    inc p.offset
    case escape
    of '"', '\\', '/': output.add escape
    of 'b': output.add '\b'
    of 'f': output.add '\f'
    of 'n': output.add '\n'
    of 'r': output.add '\r'
    of 't': output.add '\t'
    of 'u':
      if p.offset + 4 > p.text.len:
        p.error = "Invalid JSON unicode escape"
        return false
      var code = 0
      for _ in 0 ..< 4:
        let digit = hexValue(p.text[p.offset])
        inc p.offset
        if digit < 0:
          p.error = "Invalid JSON unicode escape"
          return false
        code = code shl 4 or digit
      # Each escape is encoded on its own (surrogates are not paired).
      if code <= 0x7F:
        output.add char(code)
      elif code <= 0x7FF:
        output.add char(0xC0 or code shr 6)
        output.add char(0x80 or code and 0x3F)
      else:
        output.add char(0xE0 or code shr 12)
        output.add char(0x80 or (code shr 6) and 0x3F)
        output.add char(0x80 or code and 0x3F)
    else:
      p.error = "Unsupported JSON escape"
      return false
  p.error = "Unterminated JSON string"
  false

proc c_strtod(text: cstring, finish: ptr cstring): cdouble {.importc: "strtod", header: "<stdlib.h>".}

proc parseInt64(text: string, value: var int64): bool =
  ## std::from_chars for int64: an optional '-' and decimal digits.
  var index = 0
  let negative = text.len > 0 and text[0] == '-'
  if negative: index = 1
  if index >= text.len: return false
  var magnitude: uint64 = 0
  const limit = 9223372036854775808'u64
  while index < text.len:
    let c = text[index]
    if c notin {'0' .. '9'}: return false
    let digit = uint64(ord(c) - ord('0'))
    if magnitude > (limit - digit) div 10: return false
    magnitude = magnitude * 10 + digit
    inc index
  if negative:
    value = if magnitude == limit: low(int64) else: -int64(magnitude)
  else:
    if magnitude >= limit: return false
    value = int64(magnitude)
  true

proc hasNonzeroMantissa(text: string): bool =
  for c in text:
    if c in {'e', 'E'}: return false
    if c in {'1' .. '9'}: return true
  false

proc parseNumber(p: var JsonParser): (Json, bool) =
  let begin = p.offset
  if p.text[p.offset] == '-': inc p.offset
  while p.offset < p.text.len and p.text[p.offset] in {'0' .. '9'}: inc p.offset
  var floating = false
  if p.offset < p.text.len and p.text[p.offset] == '.':
    floating = true
    inc p.offset
    while p.offset < p.text.len and p.text[p.offset] in {'0' .. '9'}: inc p.offset
  if p.offset < p.text.len and p.text[p.offset] in {'e', 'E'}:
    floating = true
    inc p.offset
    if p.offset < p.text.len and p.text[p.offset] in {'+', '-'}: inc p.offset
    while p.offset < p.text.len and p.text[p.offset] in {'0' .. '9'}: inc p.offset
  let number = p.text[begin ..< p.offset]
  if floating:
    # std::from_chars rejects out-of-range values, including nonzero values
    # that underflow to zero.
    var finish: cstring
    let value = float64(c_strtod(number.cstring, addr finish))
    let consumed = cast[int](finish) - cast[int](number.cstring)
    if number.len > 0 and consumed == number.len and value - value == 0.0 and
        (value != 0.0 or not hasNonzeroMantissa(number)):
      return (Json(kind: jkNumber, numberValue: value), true)
  else:
    var value: int64
    if parseInt64(number, value): return (jsonInteger(value), true)
  p.fail("Invalid JSON number")

proc parseValue(p: var JsonParser): (Json, bool)

proc parseObject(p: var JsonParser): (Json, bool) =
  inc p.offset
  var output = jsonObject()
  p.skipWhitespace()
  if p.consume('}'): return (output, true)
  while p.offset < p.text.len:
    var key: string
    if not p.parseString(key): return (jsonNull(), false)
    p.skipWhitespace()
    if not p.consume(':'): return p.fail("Expected ':' in JSON object")
    let (value, ok) = p.parseValue()
    if not ok: return (jsonNull(), false)
    if key notin output.objectValue: output.objectValue[key] = value
    p.skipWhitespace()
    if p.consume('}'): return (output, true)
    if not p.consume(','): return p.fail("Expected ',' or '}' in JSON object")
    p.skipWhitespace()
  p.fail("Unterminated JSON object")

proc parseArray(p: var JsonParser): (Json, bool) =
  inc p.offset
  var output = jsonArray()
  p.skipWhitespace()
  if p.consume(']'): return (output, true)
  while p.offset < p.text.len:
    let (value, ok) = p.parseValue()
    if not ok: return (jsonNull(), false)
    output.arrayValue.add value
    p.skipWhitespace()
    if p.consume(']'): return (output, true)
    if not p.consume(','): return p.fail("Expected ',' or ']' in JSON array")
    p.skipWhitespace()
  p.fail("Unterminated JSON array")

proc parseValue(p: var JsonParser): (Json, bool) =
  p.skipWhitespace()
  if p.offset >= p.text.len: return p.fail("Unexpected end of JSON input")
  let c = p.text[p.offset]
  if c == '{': return p.parseObject()
  if c == '[': return p.parseArray()
  if c == '"':
    var text: string
    if not p.parseString(text): return (jsonNull(), false)
    return (jsonString(text), true)
  if c == 't' and p.consumeLiteral("true"): return (jsonBool(true), true)
  if c == 'f' and p.consumeLiteral("false"): return (jsonBool(false), true)
  if c == 'n' and p.consumeLiteral("null"): return (jsonNull(), true)
  if c == '-' or c in {'0' .. '9'}: return p.parseNumber()
  p.fail("Unexpected JSON token")

proc parseJson*(text: string, error: var string): (Json, bool) =
  ## Parses a complete JSON document; on failure `error` receives the
  ## original parser's message.
  var p = JsonParser(text: text)
  p.skipWhitespace()
  let (value, ok) = p.parseValue()
  if not ok:
    error = p.error
    return (jsonNull(), false)
  p.skipWhitespace()
  if p.offset != p.text.len:
    error = "Unexpected trailing JSON content"
    return (jsonNull(), false)
  error = ""
  (value, true)

proc c_snprintf(buffer: cstring, size: csize_t, format: cstring): cint {.importc: "snprintf",
    header: "<stdio.h>", varargs.}

proc escapeJsonString(output: var string, text: string) =
  const hex = "0123456789abcdef"
  output.add '"'
  for c in text:
    case c
    of '"': output.add "\\\""
    of '\\': output.add "\\\\"
    of '\b': output.add "\\b"
    of '\f': output.add "\\f"
    of '\n': output.add "\\n"
    of '\r': output.add "\\r"
    of '\t': output.add "\\t"
    elif ord(c) < 0x20:
      output.add "\\u00"
      output.add hex[ord(c) shr 4]
      output.add hex[ord(c) and 15]
    else:
      output.add c
  output.add '"'

proc serialize(j: Json, output: var string) =
  case j.kind
  of jkNull: output.add "null"
  of jkBool: output.add(if j.boolValue: "true" else: "false")
  of jkInteger: output.add $j.integerValue
  of jkNumber:
    # std::ostream with setprecision(17): %.17g.
    var buffer: array[64, char]
    let count = c_snprintf(cast[cstring](addr buffer[0]), csize_t(buffer.len), "%.17g", j.numberValue)
    for i in 0 ..< count: output.add buffer[i]
  of jkString: escapeJsonString(output, j.stringValue)
  of jkArray:
    output.add '['
    for i, value in j.arrayValue:
      if i > 0: output.add ','
      serialize(value, output)
    output.add ']'
  of jkObject:
    var keys: seq[string]
    for key in j.objectValue.keys: keys.add key
    keys.sort(system.cmp[string])
    output.add '{'
    for i, key in keys:
      if i > 0: output.add ','
      escapeJsonString(output, key)
      output.add ':'
      serialize(j.objectValue[key], output)
    output.add '}'

proc serialize*(j: Json): string =
  ## The original's compact form.
  serialize(j, result)
