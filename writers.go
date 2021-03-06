package main

import (
	"bytes"
	"encoding/base64"
	"io"
	"log"
	"strconv"
	"strings"

	as "github.com/aerospike/aerospike-client-go"
)

func writeErr(wf io.Writer, errorPrefix string, s string, args [][]byte) error {
	one := ""
	two := ""
	if len(args) > 0 {
		one = string(args[0])
	}
	if len(args) > 1 {
		two = string(args[1])
	}
	log.Printf("%s Client error : %s {%s, %s}\n", errorPrefix, s, one, two)
	return write(wf, []byte("-ERR "+s+"\n"))
}

func writeByteArray(wf io.Writer, buf []byte) error {
	err := write(wf, []byte("$"+strconv.Itoa(len(buf))+"\r\n"))
	if err != nil {
		return err
	}
	err = write(wf, buf)
	if err != nil {
		return err
	}
	return write(wf, []byte("\r\n"))
}

func writeArray(wf io.Writer, array []interface{}) error {
	err := writeLine(wf, "*"+strconv.Itoa(len(array)))
	if err != nil {
		return err
	}
	for _, e := range array {
		// backward compat
		switch e.(type) {
		case string:
			s := e.(string)
			if strings.HasPrefix(s, "__64__") {
				bytes, err := base64.StdEncoding.DecodeString(s[6:])
				if err != nil {
					return err
				}
				err = writeByteArray(wf, bytes)
				if err != nil {
					return err
				}
			} else {
				err := writeByteArray(wf, []byte(s))
				if err != nil {
					return err
				}
			}
		default:
			// end of backward compat
			err := writeByteArray(wf, e.([]byte))
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func write(wf io.Writer, b []byte) error {
	if _, err := wf.Write(b); err != nil {
		return err
	}

	return nil
}

func writeLine(wf io.Writer, s string) error {
	if err := write(wf, []byte(s)); err != nil {
		return err
	}

	if err := write(wf, []byte("\r\n")); err != nil {
		return err
	}

	return nil
	// return wf([]byte(s + "\r\n"))
}

func writeValue(wf io.Writer, x interface{}) error {
	switch x.(type) {
	case int:
		return writeByteArray(wf, []byte(strconv.Itoa(x.(int))))
	// backward compat
	case string:
		s := x.(string)
		if strings.HasPrefix(s, "__64__") {
			bytes, err := base64.StdEncoding.DecodeString(s[6:])
			if err != nil {
				return err
			}
			return writeByteArray(wf, bytes)
		}
		return writeByteArray(wf, []byte(s))
	// end of backward compat
	default:
		return writeByteArray(wf, x.([]byte))
	}
}

func writeBin(wf io.Writer, rec *as.Record, binName string, nilValue string) error {
	if rec == nil {
		return writeLine(wf, nilValue)
	}
	x := rec.Bins[binName]
	if x == nil {
		return writeLine(wf, nilValue)
	}
	return writeValue(wf, x)
}

func writeBinInt(wf io.Writer, rec *as.Record, binName string) error {
	nilValue := ":0"
	if rec == nil {
		return writeLine(wf, nilValue)
	}
	x := rec.Bins[binName]
	if x == nil {
		return writeLine(wf, nilValue)
	}
	return writeLine(wf, ":"+strconv.Itoa(x.(int)))
}

func writeArrayBin(wf io.Writer, res []*as.Record, binName string, keyBinName string) error {
	l := len(res)
	if keyBinName != "" {
		l *= 2
	}
	err := writeLine(wf, "*"+strconv.Itoa(l))
	if err != nil {
		return err
	}
	for _, e := range res {
		if keyBinName != "" {
			err := writeBin(wf, e, keyBinName, "$-1")
			if err != nil {
				return err
			}
		}
		err := writeBin(wf, e, binName, "$-1")
		if err != nil {
			return err
		}
	}
	return nil
}

func encode(ctx *context, buf []byte) interface{} {
	if len(buf) < 10 {
		x, err := strconv.Atoi(string(buf))
		if err == nil {
			return x
		}
	}
	if !ctx.backwardWriteCompat {
		return buf
	}
	if bytes.IndexByte(buf, 0) == -1 {
		return string(buf)
	}
	return "__64__" + base64.StdEncoding.EncodeToString(buf)
}
