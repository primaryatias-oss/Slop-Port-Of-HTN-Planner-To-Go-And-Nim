## The std::filesystem path operations the original tooling relies on
## (libstdc++ semantics on Linux): weakly_canonical, lexically_normal and
## absolute.

import std/[os, posix, strutils]

proc status(path: string): (bool, bool) =
  ## (exists, known): a missing path or a non-directory prefix is known not
  ## to exist.
  var info: Stat
  if stat(path.cstring, info) == 0: return (true, true)
  if errno == ENOENT or errno == ENOTDIR: return (false, true)
  (false, false)

proc append*(base, element: string): string =
  ## std::filesystem::path::operator/=.
  if element.startsWith("/") or base.len == 0: element
  elif base.endsWith("/"): base & element
  else: base & "/" & element

proc c_free(p: pointer) {.importc: "free", header: "<stdlib.h>".}

proc canonical(path: string): (string, bool) =
  ## std::filesystem::canonical: an absolute path without symbolic links or
  ## dot elements; the path must exist.
  let resolved = realpath(path.cstring, nil)
  if resolved == nil: return ("", false)
  result = ($resolved, true)
  c_free(resolved)

proc elements(path: string): seq[string] =
  ## std::filesystem::path iteration: the root directory, each filename and an
  ## empty filename for a trailing separator.
  if path.startsWith("/"): result.add "/"
  for part in path.split('/'):
    if part.len > 0: result.add part
  if path.endsWith("/") and path.strip(chars = {'/'}).len > 0: result.add ""

proc lexicallyNormal*(path: string): string =
  ## std::filesystem::path::lexically_normal.
  if path.len == 0: return ""
  let absolute = path.startsWith("/")
  var stack: seq[string]
  var trailing = false
  for token in path.split('/'):
    case token
    of "": continue
    of ".": trailing = true
    of "..":
      if stack.len > 0 and stack[^1] != "..":
        stack.setLen(stack.len - 1)
        trailing = true
      elif stack.len == 0 and absolute:
        trailing = true
      else:
        stack.add ".."
        trailing = false
    else:
      stack.add token
      trailing = false
  if path.endsWith("/"): trailing = true
  if stack.len == 0:
    return if absolute: "/" else: "."
  result = stack.join("/")
  if absolute: result = "/" & result
  if trailing and stack[^1] != "..": result.add '/'

proc weaklyCanonical*(path: string): (string, bool) =
  ## std::filesystem::weakly_canonical: canonicalizes the longest existing
  ## prefix and appends the remaining elements in normal form. Fails when a
  ## status query fails for a reason other than a missing file.
  let (exists, known) = status(path)
  if exists: return canonical(path)
  if not known: return ("", false)
  let parts = elements(path)
  var current = ""
  var index = 0
  while index < parts.len:
    let candidate = append(current, parts[index])
    let (partExists, partKnown) = status(candidate)
    if not partExists:
      if not partKnown: return ("", false)
      break
    current = candidate
    inc index
  if current.len > 0:
    let (resolved, ok) = canonical(current)
    if not ok: return ("", false)
    current = resolved
  while index < parts.len:
    current = append(current, parts[index])
    inc index
  (lexicallyNormal(current), true)

proc pathKey*(path: string): string =
  ## weakly_canonical(path), or lexically_normal(path) when that fails (the
  ## key used by the original document stores).
  let (canonicalPath, ok) = weaklyCanonical(path)
  if ok: canonicalPath else: lexicallyNormal(path)

proc samePath*(a, b: string): bool = pathKey(a) == pathKey(b)

proc absolute*(path: string): (string, bool) =
  ## std::filesystem::absolute: relative paths are appended to the current
  ## directory without normalization. Fails for an empty path.
  if path.len == 0: return ("", false)
  if path.startsWith("/"): return (path, true)
  try:
    (append(getCurrentDir(), path), true)
  except OSError:
    ("", false)
