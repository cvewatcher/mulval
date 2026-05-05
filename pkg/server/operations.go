package server

import (
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"cloud.google.com/go/longrunning/autogen/longrunningpb"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func registerOperationsHTTP(mux *runtime.ServeMux, srv longrunningpb.OperationsServer) {
	must(mux.HandlePath("GET", "/api/v1/operations/**", getOrListOperationsHTTPHandler(mux, srv)))
	must(mux.HandlePath("POST", "/api/v1/operations/**:cancel", cancelOperationHTTPHandler(mux, srv)))
	must(mux.HandlePath("POST", "/api/v1/operations/**:wait", waitOperationHTTPHandler(mux, srv)))
	must(mux.HandlePath("DELETE", "/api/v1/operations/**", deleteOperationHTTPHandler(mux, srv)))
}

func getOrListOperationsHTTPHandler(mux *runtime.ServeMux, srv longrunningpb.OperationsServer) runtime.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, pathParams map[string]string) {
		path := strings.TrimPrefix(r.URL.RawPath, "/api/v1/operations/")
		if path == "" {
			path = strings.TrimPrefix(r.URL.Path, "/api/v1/operations/")
		}

		if path == "" {
			// No name after the prefix -> this is a ListOperations request.
			parent, err := url.QueryUnescape(r.URL.Query().Get("parent"))
			if err != nil {
				marshaler, _ := runtime.MarshalerForRequest(mux, r)
				runtime.HTTPError(r.Context(), mux, marshaler, w, r,
					status.Errorf(codes.InvalidArgument, "invalid parent: %v", err))
				return
			}
			var pageSize int32
			if pg := r.URL.Query().Get("page_size"); pg != "" {
				i, err := strconv.Atoi(pg)
				if err != nil {
					marshaler, _ := runtime.MarshalerForRequest(mux, r)
					runtime.HTTPError(r.Context(), mux, marshaler, w, r,
						status.Errorf(codes.InvalidArgument, "invalid page size: %v", err))
					return
				}
				pageSize = int32(i)
			}
			resp, err := srv.ListOperations(r.Context(), &longrunningpb.ListOperationsRequest{
				Name:      parent,
				PageSize:  pageSize,
				PageToken: r.URL.Query().Get("page_token"),
			})
			forwardResponse(mux, w, r, resp, err)
			return
		}

		// Name present -> this is a GetOperation request.
		name, err := url.PathUnescape(path)
		if err != nil {
			marshaler, _ := runtime.MarshalerForRequest(mux, r)
			runtime.HTTPError(r.Context(), mux, marshaler, w, r,
				status.Errorf(codes.InvalidArgument, "invalid operation name: %v", err))
			return
		}
		op, err := srv.GetOperation(r.Context(), &longrunningpb.GetOperationRequest{
			Name: name,
		})
		forwardResponse(mux, w, r, op, err)
	}
}

func cancelOperationHTTPHandler(mux *runtime.ServeMux, srv longrunningpb.OperationsServer) runtime.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, pathParams map[string]string) {
		name, err := extractOperationNameWithSuffix(r, "/api/v1/operations/", ":cancel")
		if err != nil {
			marshaler, _ := runtime.MarshalerForRequest(mux, r)
			runtime.HTTPError(r.Context(), mux, marshaler, w, r,
				status.Errorf(codes.InvalidArgument, "invalid operation name: %v", err))
			return
		}
		resp, err := srv.CancelOperation(r.Context(), &longrunningpb.CancelOperationRequest{
			Name: name,
		})
		forwardResponse(mux, w, r, resp, err)
	}
}

func waitOperationHTTPHandler(mux *runtime.ServeMux, srv longrunningpb.OperationsServer) runtime.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, pathParams map[string]string) {
		marshaler, _ := runtime.MarshalerForRequest(mux, r)

		name, err := extractOperationNameWithSuffix(r, "/api/v1/operations/", ":wait")
		if err != nil {
			marshaler, _ := runtime.MarshalerForRequest(mux, r)
			runtime.HTTPError(r.Context(), mux, marshaler, w, r,
				status.Errorf(codes.InvalidArgument, "invalid operation name: %v", err))
			return
		}

		var req longrunningpb.WaitOperationRequest
		if err := marshaler.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
			runtime.HTTPError(r.Context(), mux, marshaler, w, r,
				status.Errorf(codes.InvalidArgument, "decode request: %v", err))
			return
		}
		req.Name = name
		op, err := srv.WaitOperation(r.Context(), &req)
		forwardResponse(mux, w, r, op, err)
	}
}

func deleteOperationHTTPHandler(mux *runtime.ServeMux, srv longrunningpb.OperationsServer) runtime.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, pathParams map[string]string) {
		name, err := extractOperationName(r, "/api/v1/operations/")
		if err != nil {
			marshaler, _ := runtime.MarshalerForRequest(mux, r)
			runtime.HTTPError(r.Context(), mux, marshaler, w, r,
				status.Errorf(codes.InvalidArgument, "invalid operation name: %v", err))
			return
		}

		resp, err := srv.DeleteOperation(r.Context(), &longrunningpb.DeleteOperationRequest{
			Name: name,
		})
		forwardResponse(mux, w, r, resp, err)
	}
}

func forwardResponse(mux *runtime.ServeMux, w http.ResponseWriter, r *http.Request, msg proto.Message, err error) {
	marshaler, _ := runtime.MarshalerForRequest(mux, r)
	if err != nil {
		runtime.HTTPError(r.Context(), mux, marshaler, w, r, err)
		return
	}
	runtime.ForwardResponseMessage(r.Context(), mux, marshaler, w, r, msg)
}

func extractOperationName(r *http.Request, prefix string) (string, error) {
	// Strip "/api/v1/operations/" prefix and URL-decode the rest.
	path := strings.TrimPrefix(r.URL.RawPath, prefix)
	if path == "" {
		path = strings.TrimPrefix(r.URL.Path, prefix)
	}
	return url.PathUnescape(path)
}

func extractOperationNameWithSuffix(r *http.Request, prefix, suffix string) (string, error) {
	path := strings.TrimPrefix(r.URL.RawPath, prefix)
	if path == "" {
		path = strings.TrimPrefix(r.URL.Path, prefix)
	}
	path = strings.TrimSuffix(path, suffix)
	return url.PathUnescape(path)
}
