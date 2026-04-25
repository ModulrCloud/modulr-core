package routes

import (
	"encoding/json"
	"strings"

	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/cryptography"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/http_pack/helpers"
	"github.com/modulrcloud/modulr-core/structures"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/valyala/fasthttp"
)

// MAX_VALIDATOR_WS_ENDPOINT_PUBKEYS caps the number of pubkeys per request to keep
// the response small and protect the node from accidental fan-out abuse.
const MAX_VALIDATOR_WS_ENDPOINT_PUBKEYS = 256

func GetAccountById(ctx *fasthttp.RequestCtx) {
	accountIdRaw := ctx.UserValue("accountId")
	accountId, ok := accountIdRaw.(string)

	if !ok || accountId == "" {
		helpers.WriteErr(ctx, fasthttp.StatusBadRequest, "Invalid account id")
		return
	}

	accountBytes, err := databases.STATE.Get([]byte(accountId), nil)

	if err != nil {
		if err == leveldb.ErrNotFound {
			helpers.WriteErr(ctx, fasthttp.StatusNotFound, "Not found")
			return
		}

		helpers.WriteErr(ctx, fasthttp.StatusInternalServerError, "Failed to load account")
		return
	}

	if len(accountBytes) == 0 {
		helpers.WriteErr(ctx, fasthttp.StatusNotFound, "Not found")
		return
	}

	var account structures.Account
	if err := json.Unmarshal(accountBytes, &account); err != nil {
		helpers.WriteErr(ctx, fasthttp.StatusInternalServerError, "Failed to parse account")
		return
	}

	helpers.WriteJSON(ctx, fasthttp.StatusOK, account)
}

func GetValidatorByPubkey(ctx *fasthttp.RequestCtx) {
	pubkeyRaw := ctx.UserValue("validatorPubkey")
	pubkey, ok := pubkeyRaw.(string)

	if !ok || pubkey == "" || !cryptography.IsValidPubKey(pubkey) {
		helpers.WriteErr(ctx, fasthttp.StatusBadRequest, "Invalid validator pubkey")
		return
	}

	key := []byte(constants.DBKeyPrefixValidatorStorage + pubkey)
	raw, err := databases.STATE.Get(key, nil)

	if err != nil {
		if err == leveldb.ErrNotFound {
			helpers.WriteErr(ctx, fasthttp.StatusNotFound, "Not found")
			return
		}

		helpers.WriteErr(ctx, fasthttp.StatusInternalServerError, "Failed to load validator")
		return
	}

	if len(raw) == 0 {
		helpers.WriteErr(ctx, fasthttp.StatusNotFound, "Not found")
		return
	}

	var vs structures.ValidatorStorage
	if err := json.Unmarshal(raw, &vs); err != nil {
		helpers.WriteErr(ctx, fasthttp.StatusInternalServerError, "Failed to parse validator")
		return
	}

	helpers.WriteJSON(ctx, fasthttp.StatusOK, vs)
}

// GetValidatorWsEndpoints returns a {pubkey: wssValidatorURL} map for the requested
// validator pubkeys. Pubkeys are passed as a comma-separated list via the `pubkeys`
// query string. Unknown or invalid pubkeys are silently dropped from the response;
// known validators with an empty WSS URL are also dropped.
//
// This route exists primarily so that anchor (modulr-anchors-core) bootstrap nodes
// can resolve WSS endpoints of core quorum members when collecting ALFPs without
// having to extend the on-chain epoch rotation proof payload.
//
// Example: GET /get_validator_ws_endpoints?pubkeys=pk1,pk2,pk3
// Response: {"pk1":"wss://...","pk3":"wss://..."}
func GetValidatorWsEndpoints(ctx *fasthttp.RequestCtx) {
	raw := string(ctx.QueryArgs().Peek("pubkeys"))
	if raw == "" {
		helpers.WriteErr(ctx, fasthttp.StatusBadRequest, "missing 'pubkeys' query parameter")
		return
	}

	pubkeys := splitAndDedupPubkeys(raw, MAX_VALIDATOR_WS_ENDPOINT_PUBKEYS)
	if len(pubkeys) == 0 {
		helpers.WriteErr(ctx, fasthttp.StatusBadRequest, "no valid pubkeys provided")
		return
	}

	endpoints := make(map[string]string, len(pubkeys))

	for _, pk := range pubkeys {
		key := []byte(constants.DBKeyPrefixValidatorStorage + pk)
		raw, err := databases.STATE.Get(key, nil)
		if err != nil || len(raw) == 0 {
			continue
		}

		var vs structures.ValidatorStorage
		if json.Unmarshal(raw, &vs) != nil {
			continue
		}

		if vs.WssValidatorUrl == "" {
			continue
		}
		endpoints[pk] = vs.WssValidatorUrl
	}

	helpers.WriteJSON(ctx, fasthttp.StatusOK, endpoints)
}

// splitAndDedupPubkeys parses a comma-separated list of pubkeys, drops empties
// and invalid entries, deduplicates, and caps to `limit` items in input order.
func splitAndDedupPubkeys(raw string, limit int) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))

	for _, p := range parts {
		pk := strings.TrimSpace(p)
		if pk == "" {
			continue
		}
		if !cryptography.IsValidPubKey(pk) {
			continue
		}
		if _, dup := seen[pk]; dup {
			continue
		}
		seen[pk] = struct{}{}
		out = append(out, pk)
		if len(out) >= limit {
			break
		}
	}
	return out
}
