package accounts

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/codex-switch/admin-go/internal/platform"
)

const maxAccountAliasLength = 16

func normalizeAccountAlias(details object) error {
	raw, present := details["alias"]
	if !present {
		return nil
	}
	alias, valid := raw.(string)
	alias = strings.TrimSpace(alias)
	if !valid || utf8.RuneCountInString(alias) > maxAccountAliasLength ||
		strings.ContainsFunc(alias, unicode.IsControl) {
		return platform.NewError(400, "client.accounts.aliasInvalid")
	}
	details["alias"] = alias
	return nil
}

// Older clients omit the new field; an explicitly empty alias still clears it.
func preserveAccountAlias(next, previous object) object {
	next = copyObject(next)
	if !has(next, "alias") && has(previous, "alias") {
		next["alias"] = previous["alias"]
	}
	return next
}
