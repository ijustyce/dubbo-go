/*
 * Licensed to the Apache Software Foundation (ASF) under one or more
 * contributor license agreements.  See the NOTICE file distributed with
 * this work for additional information regarding copyright ownership.
 * The ASF licenses this file to You under the Apache License, Version 2.0
 * (the "License"); you may not use this file except in compliance with
 * the License.  You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package server

import (
	"context"
	"net/http"
	"reflect"
	"strconv"
	"strings"
)

import (
	perrors "github.com/pkg/errors"
)

import (
	"github.com/apache/dubbo-go/common"
	"github.com/apache/dubbo-go/common/logger"
	"github.com/apache/dubbo-go/protocol"
	"github.com/apache/dubbo-go/protocol/invocation"
	rest_config "github.com/apache/dubbo-go/protocol/rest/config"
)

type RestServer interface {
	// start rest server
	Start(url common.URL)
	// deploy a http api
	Deploy(restMethodConfig *rest_config.RestMethodConfig, routeFunc func(request RestServerRequest, response RestServerResponse))
	// unDeploy a http api
	UnDeploy(restMethodConfig *rest_config.RestMethodConfig)
	// destroy rest server
	Destroy()
}

// RestServerRequest interface
type RestServerRequest interface {
	// Get the Ptr of http.Request
	RawRequest() *http.Request
	// Get the path parameter by name
	PathParameter(name string) string
	// Get the map of the path parameters
	PathParameters() map[string]string
	// Get the query parameter by name
	QueryParameter(name string) string
	// Get the map of query parameters
	QueryParameters(name string) []string
	// Get the body parameter of name
	BodyParameter(name string) (string, error)
	// Get the header parameter of name
	HeaderParameter(name string) string
	// ReadEntity checks the Accept header and reads the content into the entityPointer.
	ReadEntity(entityPointer interface{}) error
}

// RestServerResponse interface
type RestServerResponse interface {
	http.ResponseWriter
	// WriteError writes the http status and the error string on the response. err can be nil.
	// Return an error if writing was not succesful.
	WriteError(httpStatus int, err error) (writeErr error)
	// WriteEntity marshals the value using the representation denoted by the Accept Header.
	WriteEntity(value interface{}) error
}

// A route function will be invoked by http server
func GetRouteFunc(invoker protocol.Invoker, methodConfig *rest_config.RestMethodConfig) func(req RestServerRequest, resp RestServerResponse) {
	return func(req RestServerRequest, resp RestServerResponse) {
		var (
			err  error
			args []interface{}
		)
		svc := common.ServiceMap.GetService(invoker.GetUrl().Protocol, strings.TrimPrefix(invoker.GetUrl().Path, "/"))
		// get method
		method := svc.Method()[methodConfig.MethodName]
		argsTypes := method.ArgsType()
		replyType := method.ReplyType()
		if (len(argsTypes) == 1 || len(argsTypes) == 2 && replyType == nil) &&
			argsTypes[0].String() == "[]interface {}" {
			args = getArgsInterfaceFromRequest(req, methodConfig)
		} else {
			args = getArgsFromRequest(req, argsTypes, methodConfig)
		}
		result := invoker.Invoke(context.Background(), invocation.NewRPCInvocation(methodConfig.MethodName, args, make(map[string]string)))
		if result.Error() != nil {
			err = resp.WriteError(http.StatusInternalServerError, result.Error())
			if err != nil {
				logger.Errorf("[Go Restful] WriteError error:%v", err)
			}
			return
		}
		err = resp.WriteEntity(result.Result())
		if err != nil {
			logger.Errorf("[Go Restful] WriteEntity error:%v", err)
		}
	}
}

// when service function like GetUser(req []interface{}, rsp *User) error
// use this method to get arguments
func getArgsInterfaceFromRequest(req RestServerRequest, methodConfig *rest_config.RestMethodConfig) []interface{} {
	argsMap := make(map[int]interface{}, 8)
	maxKey := 0
	for k, v := range methodConfig.PathParamsMap {
		if maxKey < k {
			maxKey = k
		}
		argsMap[k] = req.PathParameter(v)
	}
	for k, v := range methodConfig.QueryParamsMap {
		if maxKey < k {
			maxKey = k
		}
		params := req.QueryParameters(v)
		if len(params) == 1 {
			argsMap[k] = params[0]
		} else {
			argsMap[k] = params
		}
	}
	for k, v := range methodConfig.HeadersMap {
		if maxKey < k {
			maxKey = k
		}
		argsMap[k] = req.HeaderParameter(v)
	}
	if methodConfig.Body >= 0 {
		if maxKey < methodConfig.Body {
			maxKey = methodConfig.Body
		}
		m := make(map[string]interface{})
		// TODO read as a slice
		if err := req.ReadEntity(&m); err != nil {
			logger.Warnf("[Go restful] Read body entity as map[string]interface{} error:%v", perrors.WithStack(err))
		} else {
			argsMap[methodConfig.Body] = m
		}
	}
	args := make([]interface{}, maxKey+1)
	for k, v := range argsMap {
		if k >= 0 {
			args[k] = v
		}
	}
	return args
}

// get arguments from server.RestServerRequest
func getArgsFromRequest(req RestServerRequest, argsTypes []reflect.Type, methodConfig *rest_config.RestMethodConfig) []interface{} {
	argsLength := len(argsTypes)
	args := make([]interface{}, argsLength)
	for i, t := range argsTypes {
		args[i] = reflect.Zero(t).Interface()
	}
	assembleArgsFromPathParams(methodConfig, argsLength, argsTypes, req, args)
	assembleArgsFromQueryParams(methodConfig, argsLength, argsTypes, req, args)
	assembleArgsFromBody(methodConfig, argsTypes, req, args)
	assembleArgsFromHeaders(methodConfig, req, argsLength, argsTypes, args)
	return args
}

// assemble arguments from headers
func assembleArgsFromHeaders(methodConfig *rest_config.RestMethodConfig, req RestServerRequest, argsLength int, argsTypes []reflect.Type, args []interface{}) {
	for k, v := range methodConfig.HeadersMap {
		param := req.HeaderParameter(v)
		if k < 0 || k >= argsLength {
			logger.Errorf("[Go restful] Header param parse error, the args:%v doesn't exist", k)
			continue
		}
		t := argsTypes[k]
		if t.Kind() == reflect.Ptr {
			t = t.Elem()
		}
		if t.Kind() == reflect.String {
			args[k] = param
		} else {
			logger.Errorf("[Go restful] Header param parse error, the args:%v of type isn't string", k)
		}
	}
}

// assemble arguments from body
func assembleArgsFromBody(methodConfig *rest_config.RestMethodConfig, argsTypes []reflect.Type, req RestServerRequest, args []interface{}) {
	if methodConfig.Body >= 0 && methodConfig.Body < len(argsTypes) {
		t := argsTypes[methodConfig.Body]
		kind := t.Kind()
		if kind == reflect.Ptr {
			t = t.Elem()
		}
		var ni interface{}
		if t.String() == "[]interface {}" {
			ni = make([]map[string]interface{}, 0)
		} else if t.String() == "interface {}" {
			ni = make(map[string]interface{})
		} else {
			n := reflect.New(t)
			if n.CanInterface() {
				ni = n.Interface()
			}
		}
		if err := req.ReadEntity(&ni); err != nil {
			logger.Errorf("[Go restful] Read body entity error:%v", err)
		} else {
			args[methodConfig.Body] = ni
		}
	}
}

// assemble arguments from query params
func assembleArgsFromQueryParams(methodConfig *rest_config.RestMethodConfig, argsLength int, argsTypes []reflect.Type, req RestServerRequest, args []interface{}) {
	var (
		err   error
		param interface{}
		i64   int64
	)
	for k, v := range methodConfig.QueryParamsMap {
		if k < 0 || k >= argsLength {
			logger.Errorf("[Go restful] Query param parse error, the args:%v doesn't exist", k)
			continue
		}
		t := argsTypes[k]
		kind := t.Kind()
		if kind == reflect.Ptr {
			t = t.Elem()
		}
		if kind == reflect.Slice {
			param = req.QueryParameters(v)
		} else if kind == reflect.String {
			param = req.QueryParameter(v)
		} else if kind == reflect.Int {
			param, err = strconv.Atoi(req.QueryParameter(v))
		} else if kind == reflect.Int32 {
			i64, err = strconv.ParseInt(req.QueryParameter(v), 10, 32)
			if err == nil {
				param = int32(i64)
			}
		} else if kind == reflect.Int64 {
			param, err = strconv.ParseInt(req.QueryParameter(v), 10, 64)
		} else {
			logger.Errorf("[Go restful] Query param parse error, the args:%v of type isn't int or string or slice", k)
			continue
		}
		if err != nil {
			logger.Errorf("[Go restful] Query param parse error, error is %v", err)
			continue
		}
		args[k] = param
	}
}

// assemble arguments from path params
func assembleArgsFromPathParams(methodConfig *rest_config.RestMethodConfig, argsLength int, argsTypes []reflect.Type, req RestServerRequest, args []interface{}) {
	var (
		err   error
		param interface{}
		i64   int64
	)
	for k, v := range methodConfig.PathParamsMap {
		if k < 0 || k >= argsLength {
			logger.Errorf("[Go restful] Path param parse error, the args:%v doesn't exist", k)
			continue
		}
		t := argsTypes[k]
		kind := t.Kind()
		if kind == reflect.Ptr {
			t = t.Elem()
		}
		if kind == reflect.Int {
			param, err = strconv.Atoi(req.PathParameter(v))
		} else if kind == reflect.Int32 {
			i64, err = strconv.ParseInt(req.PathParameter(v), 10, 32)
			if err == nil {
				param = int32(i64)
			}
		} else if kind == reflect.Int64 {
			param, err = strconv.ParseInt(req.PathParameter(v), 10, 64)
		} else if kind == reflect.String {
			param = req.PathParameter(v)
		} else {
			logger.Warnf("[Go restful] Path param parse error, the args:%v of type isn't int or string", k)
			continue
		}
		if err != nil {
			logger.Errorf("[Go restful] Path param parse error, error is %v", err)
			continue
		}
		args[k] = param
	}
}
