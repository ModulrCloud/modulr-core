package routes

import (
	"encoding/json"

	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/http_pack/helpers"
	"github.com/modulrcloud/modulr-core/structures"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/valyala/fasthttp"
)

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
