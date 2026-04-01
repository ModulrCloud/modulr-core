package routes

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/http_pack/helpers"
	"github.com/modulrcloud/modulr-core/structures"

	"github.com/valyala/fasthttp"
)

func GetHeightAttestation(ctx *fasthttp.RequestCtx) {
	heightRaw := ctx.UserValue("height")
	heightStr, ok := heightRaw.(string)

	if !ok || heightStr == "" {
		helpers.WriteErr(ctx, fasthttp.StatusBadRequest, "Invalid height")
		return
	}

	height, err := strconv.Atoi(heightStr)
	if err != nil {
		helpers.WriteErr(ctx, fasthttp.StatusBadRequest, "Invalid height")
		return
	}

	key := []byte(fmt.Sprintf("%s%d", constants.DBKeyPrefixHeightAttestation, height))
	raw, err := databases.FINALIZATION_VOTING_STATS.Get(key, nil)
	if err != nil {
		helpers.WriteErr(ctx, fasthttp.StatusNotFound, "Not found")
		return
	}

	var proof structures.HeightAttestation
	if json.Unmarshal(raw, &proof) != nil {
		helpers.WriteErr(ctx, fasthttp.StatusInternalServerError, "Failed to parse attestation")
		return
	}

	helpers.WriteJSON(ctx, fasthttp.StatusOK, proof)
}
