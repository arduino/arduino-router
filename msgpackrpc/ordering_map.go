// This file is part of arduino-router.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package msgpackrpc

import "github.com/vmihailenco/msgpack/v5"

type Value struct {
	Key   string
	Value any
}

type OrderedMap []Value

func (o OrderedMap) EncodeMsgpack(enc *msgpack.Encoder) error {
	if err := enc.EncodeMapLen(len(o)); err != nil {
		return err
	}
	for _, kv := range o {
		if err := enc.Encode(kv.Key); err != nil {
			return err
		}
		if err := enc.Encode(kv.Value); err != nil {
			return err
		}
	}
	return nil
}

func (o *OrderedMap) DecodeMsgpack(dec *msgpack.Decoder) error {
	n, err := dec.DecodeMapLen()
	if err != nil {
		return err
	}

	if n == -1 {
		*o = nil
		return nil
	}

	*o = make(OrderedMap, n)
	for i := range n {
		k, err := dec.DecodeString()
		if err != nil {
			return err
		}
		v, err := dec.DecodeInterface()
		if err != nil {
			return err
		}
		(*o)[i] = Value{Key: k, Value: v}
	}

	return nil
}
