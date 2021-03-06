package main

import (
	"io"
	"math/rand"
	"strconv"
	"time"

	as "github.com/aerospike/aerospike-client-go"
	ase "github.com/aerospike/aerospike-client-go/types"
)

const letterBytes = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

const MAIN_SUFFIX = "____MAIN____"
const ROOT_BIN_NAME = "z"
const VALUE_BIN_NAME = "v"
const MAIN_KEY_BIN_NAME = "m"
const SECOND_KEY_BIN_NAME = "s"

func randStringBytes(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = letterBytes[rand.Intn(len(letterBytes))]
	}
	return string(b)
}

func now() int64 {
	return time.Now().UnixNano()
}

func formatCompositeKey(ctx *context, key string, field string) (*as.Key, error) {
	return as.NewKey(ctx.ns, ctx.set, "composite_"+key+"_"+field)
}

func compositeExists(ctx *context, k string) (*string, error) {
	if ctx.expandedMapCache != nil {
		v, err := ctx.expandedMapCache.Get([]byte(k))
		if err == nil {
			s := string(v)
			return &s, nil
		}
	}
	key, err := formatCompositeKey(ctx, k, MAIN_SUFFIX)
	if err != nil {
		return nil, err
	}
	rec, err := ctx.client.Get(ctx.readPolicy, key, ROOT_BIN_NAME)
	if err != nil {
		return nil, err
	}
	if rec != nil && rec.Bins[ROOT_BIN_NAME] != nil {
		s := rec.Bins[ROOT_BIN_NAME].(string)
		if ctx.expandedMapCache != nil {
			ctx.expandedMapCache.Set([]byte(k), []byte(s), ctx.expandedMapCacheTTL)
		}
		return &s, nil
	}
	return nil, nil
}

func compositeExistsOrCreate(ctx *context, k string, ttl int) (*string, bool, error) {
	return _compositeExistsOrCreate(ctx, k, ttl, true)
}

func _compositeExistsOrCreate(ctx *context, k string, ttl int, canRetry bool) (*string, bool, error) {
	suffixedKey, err := compositeExists(ctx, k)
	if err != nil {
		return nil, false, err
	}
	if suffixedKey != nil {
		if ttl == -1 {
			return suffixedKey, false, nil
		}
		key, err := formatCompositeKey(ctx, k, MAIN_SUFFIX)
		if err != nil {
			return nil, false, err
		}
		err = ctx.client.Touch(fillWritePolicyEx(ctx, ttl, false), key)
		if err != nil {
			return nil, false, err
		}
		return suffixedKey, false, nil
	}
	kk := k + "_" + randStringBytes(8)
	key, err := formatCompositeKey(ctx, k, MAIN_SUFFIX)
	if err != nil {
		return nil, false, err
	}
	rec := as.BinMap{
		ROOT_BIN_NAME: kk,
		"created_at":  now(),
	}
	err = ctx.client.Put(fillWritePolicyEx(ctx, ttl, true), key, rec)
	if err != nil {
		if errResultCode(err) == ase.KEY_EXISTS_ERROR && canRetry {
			return _compositeExistsOrCreate(ctx, k, ttl, false)
		}
		return nil, false, err
	}
	if ctx.expandedMapCache != nil {
		ctx.expandedMapCache.Set([]byte(k), []byte(kk), ctx.expandedMapCacheTTL)
	}
	return &kk, true, nil
}

func cmdExpandedMapHGET(wf io.Writer, ctx *context, args [][]byte) error {
	suffixedKey, err := compositeExists(ctx, string(args[0]))
	if err != nil {
		return err
	}
	if suffixedKey == nil {
		return writeLine(wf, "$-1")
	}

	key, err := formatCompositeKey(ctx, *suffixedKey, string(args[1]))
	if err != nil {
		return err
	}
	rec, err := ctx.client.Get(ctx.readPolicy, key, VALUE_BIN_NAME)
	if err != nil {
		return err
	}
	return writeBin(wf, rec, VALUE_BIN_NAME, "$-1")
}

func cmdExpandedMapHSET(wf io.Writer, ctx *context, args [][]byte) error {
	suffixedKey, _, err := compositeExistsOrCreate(ctx, string(args[0]), -1)
	if err != nil {
		return err
	}
	key, err := formatCompositeKey(ctx, *suffixedKey, string(args[1]))
	if err != nil {
		return err
	}
	exists, err := ctx.client.Exists(ctx.readPolicy, key)
	if err != nil {
		return err
	}
	rec := as.BinMap{
		MAIN_KEY_BIN_NAME:   *suffixedKey,
		SECOND_KEY_BIN_NAME: string(args[1]),
		VALUE_BIN_NAME:      encode(ctx, args[2]),
		"created_at":        now(),
	}
	err = ctx.client.Put(ctx.writePolicy, key, rec)
	if err != nil {
		return err
	}
	if exists {
		return writeLine(wf, ":0")
	}
	return writeLine(wf, ":1")
}

func cmdExpandedMapHDEL(wf io.Writer, ctx *context, args [][]byte) error {
	suffixedKey, err := compositeExists(ctx, string(args[0]))
	if err != nil {
		return err
	}
	if suffixedKey == nil {
		return writeLine(wf, ":0")
	}
	key, err := formatCompositeKey(ctx, *suffixedKey, string(args[1]))
	if err != nil {
		return err
	}
	existed, err := ctx.client.Delete(ctx.writePolicy, key)
	if err != nil {
		return err
	}
	if existed {
		return writeLine(wf, ":1")
	}
	return writeLine(wf, ":0")
}

func cmdExpandedMapEXPIRE(wf io.Writer, ctx *context, args [][]byte) error {
	ttl, err := strconv.Atoi(string(args[1]))
	if err != nil {
		return err
	}
	key, err := formatCompositeKey(ctx, string(args[0]), MAIN_SUFFIX)
	if err != nil {
		return err
	}
	err = ctx.client.Touch(fillWritePolicyEx(ctx, ttl, false), key)
	if err == nil {
		return writeLine(wf, ":1")
	}
	if errResultCode(err) != ase.KEY_NOT_FOUND_ERROR {
		return err
	}
	return cmdEXPIRE(wf, ctx, args)
}

func cmdExpandedMapTTL(wf io.Writer, ctx *context, args [][]byte) error {
	key, err := formatCompositeKey(ctx, string(args[0]), MAIN_SUFFIX)
	if err != nil {
		return err
	}
	rec, err := ctx.client.GetHeader(ctx.readPolicy, key)
	if err != nil {
		return err
	}
	if rec != nil {
		return writeLine(wf, ":"+strconv.FormatUint(uint64(rec.Expiration), 10))
	}
	return cmdTTL(wf, ctx, args)
}

func cmdExpandedMapDEL(wf io.Writer, ctx *context, args [][]byte) error {
	key, err := formatCompositeKey(ctx, string(args[0]), MAIN_SUFFIX)
	if err != nil {
		return err
	}
	existed, err := ctx.client.Delete(ctx.writePolicy, key)
	if err != nil {
		return err
	}
	if existed {
		if ctx.expandedMapCache != nil {
			ctx.expandedMapCache.Del(args[0])
		}
		return writeLine(wf, ":1")
	}
	return cmdDEL(wf, ctx, args)
}

func cmdExpandedMapHMSET(wf io.Writer, ctx *context, args [][]byte) error {
	suffixedKey, _, err := compositeExistsOrCreate(ctx, string(args[0]), -1)
	if err != nil {
		return err
	}
	for i := 1; i+1 < len(args); i += 2 {
		key, err := formatCompositeKey(ctx, *suffixedKey, string(args[i]))
		if err != nil {
			return err
		}
		rec := as.BinMap{
			MAIN_KEY_BIN_NAME:   *suffixedKey,
			SECOND_KEY_BIN_NAME: string(args[i]),
			VALUE_BIN_NAME:      encode(ctx, args[i+1]),
			"created_at":        now(),
		}
		err = ctx.client.Put(fillWritePolicyEx(ctx, ctx.expandedMapDefaultTTL, false), key, rec)
		if err != nil {
			return err
		}
	}
	return writeLine(wf, "+OK")
}

func cmdExpandedMapHMGET(wf io.Writer, ctx *context, args [][]byte) error {
	suffixedKey, err := compositeExists(ctx, string(args[0]))
	if err != nil {
		return err
	}
	res := make([]*as.Record, len(args)-1)
	if suffixedKey != nil {
		for i := 0; i < len(args)-1; i++ {
			key, err := formatCompositeKey(ctx, *suffixedKey, string(args[i+1]))
			if err != nil {
				return err
			}
			rec, err := ctx.client.Get(ctx.readPolicy, key, VALUE_BIN_NAME)
			if err != nil {
				return err
			}
			res[i] = rec
		}
	}
	return writeArrayBin(wf, res, VALUE_BIN_NAME, "")
}

func cmdExpandedMapHGETALL(wf io.Writer, ctx *context, args [][]byte) error {
	suffixedKey, err := compositeExists(ctx, string(args[0]))
	if err != nil {
		return err
	}
	if suffixedKey == nil {
		return writeArray(wf, make([]interface{}, 0))
	}
	statment := as.NewStatement(ctx.ns, ctx.set)
	statment.Addfilter(as.NewEqualFilter(MAIN_KEY_BIN_NAME, *suffixedKey))
	recordset, err := ctx.client.Query(nil, statment)
	if err != nil {
		return err
	}
	out := make([]*as.Record, 0)
	for res := range recordset.Results() {
		if res.Err != nil {
			return res.Err
		}
		out = append(out, res.Record)
	}
	return writeArrayBin(wf, out, VALUE_BIN_NAME, SECOND_KEY_BIN_NAME)
}

func compositeIncr(wf io.Writer, ctx *context, suffixedKey *string, field string, value int) error {
	key, err := formatCompositeKey(ctx, *suffixedKey, field)
	if err != nil {
		return err
	}
	rec, err := ctx.client.Operate(fillWritePolicyEx(ctx, ctx.expandedMapDefaultTTL, false), key, as.PutOp(as.NewBin(MAIN_KEY_BIN_NAME, *suffixedKey)), as.PutOp(as.NewBin(SECOND_KEY_BIN_NAME, field)), as.AddOp(as.NewBin(VALUE_BIN_NAME, value)), as.GetOpForBin(VALUE_BIN_NAME))
	if err != nil {
		if errResultCode(err) == ase.BIN_TYPE_ERROR {
			return writeLine(wf, "$-1")
		}
		return err
	}
	return writeBinInt(wf, rec, VALUE_BIN_NAME)
}

func cmdExpandedMapHINCRBYEX(wf io.Writer, ctx *context, args [][]byte) error {
	incr, err := strconv.Atoi(string(args[2]))
	if err != nil {
		return err
	}
	ttl, err := strconv.Atoi(string(args[3]))
	if err != nil {
		return err
	}
	suffixedKey, _, err := compositeExistsOrCreate(ctx, string(args[0]), ttl)
	if err != nil {
		return err
	}
	return compositeIncr(wf, ctx, suffixedKey, string(args[1]), incr)
}

func cmdExpandedMapHINCRBY(wf io.Writer, ctx *context, args [][]byte) error {
	incr, err := strconv.Atoi(string(args[2]))
	if err != nil {
		return err
	}
	suffixedKey, _, err := compositeExistsOrCreate(ctx, string(args[0]), -1)
	if err != nil {
		return err
	}
	return compositeIncr(wf, ctx, suffixedKey, string(args[1]), incr)
}

func cmdExpandedMapHMINCRBYEX(wf io.Writer, ctx *context, args [][]byte) error {
	ttl, err := strconv.Atoi(string(args[1]))
	if err != nil {
		return err
	}
	suffixedKey, _, err := compositeExistsOrCreate(ctx, string(args[0]), ttl)
	if err != nil {
		return err
	}
	if len(args) > 2 {
		a := args[2:]
		for i := 0; i+1 < len(a); i += 2 {
			incr, err := strconv.Atoi(string(a[i+1]))
			if err != nil {
				return err
			}
			key, err := formatCompositeKey(ctx, *suffixedKey, string(a[i]))
			if err != nil {
				return err
			}
			_, err = ctx.client.Operate(fillWritePolicyEx(ctx, ctx.expandedMapDefaultTTL, false), key, as.PutOp(as.NewBin(MAIN_KEY_BIN_NAME, *suffixedKey)), as.PutOp(as.NewBin(SECOND_KEY_BIN_NAME, string(a[i]))), as.AddOp(as.NewBin(VALUE_BIN_NAME, incr)))
			if err != nil {
				return err
			}
		}
	}
	return writeLine(wf, "+OK")
}
