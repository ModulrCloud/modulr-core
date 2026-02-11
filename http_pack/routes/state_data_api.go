package routes

import (
	"encoding/json"
	"strings"

	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/threads"

	"github.com/ethereum/go-ethereum/common"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/valyala/fasthttp"
)

func GetAccountById(ctx *fasthttp.RequestCtx) {

	ctx.Response.Header.Set("Access-Control-Allow-Origin", "*")

	accountIdRaw := ctx.UserValue("accountId")
	accountId, ok := accountIdRaw.(string)

	if !ok || accountId == "" {
		ctx.SetStatusCode(fasthttp.StatusBadRequest)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err": "Invalid account id"}`))
		return
	}

	// EVM lookup path: if the identifier looks like an EVM address.
	if strings.HasPrefix(accountId, "0x") || strings.HasPrefix(accountId, "0X") {
		if common.IsHexAddress(accountId) {
			addr := common.HexToAddress(accountId)
			threads.EVMLock()
			defer threads.EVMUnlock()
			r, err := threads.EVMRunnerUnsafe()
			if err != nil {
				ctx.SetStatusCode(fasthttp.StatusInternalServerError)
				ctx.SetContentType("application/json")
				ctx.Write([]byte(`{"err":"Failed to init EVM"}`))
				return
			}
			st := r.StateDB()
			balWei := st.GetBalance(addr).ToBig()
			nonce := st.GetNonce(addr)
			code := st.GetCode(addr)

			// Keep the legacy account fields expected by existing explorer code and api_docs.md:
			// balance/nonce/initiatedTransactions/successfulInitiatedTransactions
			// For EVM accounts, "balance" is represented as whole-ether floor (uint64) for legacy compatibility.
			resp := map[string]any{
				"type":    "evm",
				"id":      addr.Hex(),
				"balance": etherFloorUint64(balWei),
				"nonce":   nonce,
				// Ethereum nonce increments for every included tx from the sender (even if it reverts),
				// so it's the closest "initiated tx count" we can expose without extra per-address indexes.
				"initiatedTransactions":           nonce,
				"successfulInitiatedTransactions": nonce,
				// EVM extension fields:
				"evm": map[string]any{
					"address":    addr.Hex(),
					"balanceWei": balWei.String(),                // exact, decimal
					"balanceEth": formatWeiToEtherString(balWei), // human-readable string
					"hasCode":    len(code) > 0,
					"codeSize":   len(code),
				},
			}
			b, _ := json.Marshal(resp)
			ctx.SetStatusCode(fasthttp.StatusOK)
			ctx.SetContentType("application/json")
			ctx.Write(b)
			return
		}
	}

	accountBytes, err := databases.STATE.Get([]byte(accountId), nil)

	if err != nil {
		if err == leveldb.ErrNotFound {
			ctx.SetStatusCode(fasthttp.StatusNotFound)
			ctx.SetContentType("application/json")
			ctx.Write([]byte(`{"err": "Not found"}`))
			return
		}

		ctx.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err": "Failed to load account"}`))
		return
	}

	if len(accountBytes) == 0 {
		ctx.SetStatusCode(fasthttp.StatusNotFound)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err": "Not found"}`))
		return
	}

	var account structures.Account
	if err := json.Unmarshal(accountBytes, &account); err != nil {
		ctx.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err": "Failed to parse account"}`))
		return
	}

	// Keep legacy fields as in api_docs.md, but also include "type"/"id" for the UI.
	response, err := json.Marshal(map[string]any{
		"type":                            "native",
		"id":                              accountId,
		"balance":                         account.Balance,
		"nonce":                           account.Nonce,
		"initiatedTransactions":           account.InitiatedTransactions,
		"successfulInitiatedTransactions": account.SuccessfulInitiatedTransactions,
	})
	if err != nil {
		ctx.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err": "Failed to encode account"}`))
		return
	}

	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetContentType("application/json")
	ctx.Write(response)
}
