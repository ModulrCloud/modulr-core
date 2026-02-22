package routes

import (
	"encoding/json"

	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/cryptography"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/http_pack/helpers"
	"github.com/modulrcloud/modulr-core/structures"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/valyala/fasthttp"
)

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
