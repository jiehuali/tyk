package python

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/TykTechnologies/tyk/apidef"
	"github.com/TykTechnologies/tyk/config"
	"github.com/TykTechnologies/tyk/gateway"
	"github.com/TykTechnologies/tyk/test"
	"github.com/TykTechnologies/tyk/user"
)

var pkgPath string

func init() {
	_, filename, _, _ := runtime.Caller(0)
	pkgPath = filepath.Dir(filename) + "./../../"
}

var pythonBundleWithAuthCheck = map[string]string{
	"manifest.json": `
		{
		    "file_list": [
		        "middleware.py"
		    ],
		    "custom_middleware": {
		        "driver": "python",
		        "auth_check": {
		            "name": "MyAuthHook"
		        }
		    }
		}
	`,
	"middleware.py": `
from tyk.decorators import *
from gateway import TykGateway as tyk

@Hook
def MyAuthHook(request, session, metadata, spec):
    auth_header = request.get_header('Authorization')
    if auth_header == 'valid_token':
        session.rate = 1000.0
        session.per = 1.0
        session.quota_max = 1
        session.quota_renewal_rate = 60
        metadata["token"] = "valid_token"
    elif auth_header == 'policy':
    	session.apply_policy_id = request.get_header('Policy')
    	metadata["token"] = "policy"

    return request, session, metadata

	`,
}

var pythonBundleWithPostHook = map[string]string{
	"manifest.json": `
		{
		    "file_list": [
		        "middleware.py"
		    ],
		    "custom_middleware": {
		        "driver": "python",
		        "post": [{
		            "name": "MyPostHook"
		        }]
		    }
		}
	`,
	"middleware.py": `
from tyk.decorators import *
from gateway import TykGateway as tyk
import json

@Hook
def MyPostHook(request, session, spec):
    if "testkey" not in session.metadata.keys():
        request.object.return_overrides.response_code = 400
        request.object.return_overrides.response_error = "'testkey' not found in metadata"
        return request, session
    nested_data = json.loads(session.metadata["testkey"])
    if "nestedkey" not in nested_data:
        request.object.return_overrides.response_code = 400
        request.object.return_overrides.response_error = "'nestedkey' not found in nested metadata"
        return request, session
    if "stringkey" not in session.metadata.keys():
        request.object.return_overrides.response_code = 400
        request.object.return_overrides.response_error = "'stringkey' not found in metadata"
        return request, session
    stringkey = session.metadata["stringkey"]
    if stringkey != "testvalue":
        request.object.return_overrides.response_code = 400
        request.object.return_overrides.response_error = "'stringkey' value doesn't match"
        return request, session	
    return request, session

`,
}

var pythonPostRequestTransform = map[string]string{
	"manifest.json": `
		{
		    "file_list": [
		        "middleware.py"
		    ],
		    "custom_middleware": {
		        "driver": "python",
		        "post": [{
		            "name": "MyPostHook"
		        }]
		    }
		}
	`,
	"middleware.py": `
from tyk.decorators import *
from gateway import TykGateway as tyk
import json

@Hook
def MyPostHook(request, session, spec):
	
	
	if request.object.url == "/test2":
		if request.object.method != "POST":
			request.object.return_overrides.response_code = 500
			request.object.return_overrides.response_error = "'invalid method type'"
			return request, session
		request.object.url = "tyk://test-api-2/newpath"
		request.object.method = "GET"

	return request , session
`,
}

var pythonBundleWithPreHook = map[string]string{
	"manifest.json": `
		{
		    "file_list": [
		        "middleware.py"
		    ],
		    "custom_middleware": {
		        "driver": "python",
		        "pre": [{
		            "name": "MyPreHook"
		        }]
		    }
		}
	`,
	"middleware.py": `
from tyk.decorators import *
from gateway import TykGateway as tyk

@Hook
def MyPreHook(request, session, metadata, spec):
    content_type = request.get_header("Content-Type")
    if "json" in content_type:
      if len(request.object.raw_body) <= 0:
        request.object.return_overrides.response_code = 400
        request.object.return_overrides.response_error = "Raw body field is empty"
        return request, session, metadata
      if "{}" not in request.object.body:
        request.object.return_overrides.response_code = 400
        request.object.return_overrides.response_error = "Body field doesn't match"
        return request, session, metadata
    if "multipart" in content_type:
      if len(request.object.body) != 0:
        request.object.return_overrides.response_code = 400
        request.object.return_overrides.response_error = "Body field isn't empty"
      if len(request.object.raw_body) <= 0:
        request.object.return_overrides.response_code = 400
        request.object.return_overrides.response_error = "Raw body field is empty"
    return request, session, metadata

`,
}

func TestMain(m *testing.M) {
	os.Exit(gateway.InitTestMain(context.Background(), m))
}

func TestPythonBundles(t *testing.T) {
	ts := gateway.StartTest(gateway.TestConfig{
		CoprocessConfig: config.CoProcessConfig{
			EnableCoProcess:  true,
			PythonPathPrefix: pkgPath,
		}})
	defer ts.Close()

	authCheckBundle := gateway.RegisterBundle("python_with_auth_check", pythonBundleWithAuthCheck)
	postHookBundle := gateway.RegisterBundle("python_with_post_hook", pythonBundleWithPostHook)
	preHookBundle := gateway.RegisterBundle("python_with_pre_hook", pythonBundleWithPreHook)
	postRequestTransformHookBundle := gateway.RegisterBundle("python_post_with_request_transform_hook", pythonPostRequestTransform)

	t.Run("Single-file bundle with authentication hook", func(t *testing.T) {
		gateway.BuildAndLoadAPI(func(spec *gateway.APISpec) {
			spec.Proxy.ListenPath = "/test-api/"
			spec.UseKeylessAccess = false
			spec.EnableCoProcessAuth = true
			spec.CustomMiddlewareBundle = authCheckBundle
			spec.VersionData.NotVersioned = true
		})

		time.Sleep(1 * time.Second)

		validAuth := map[string]string{"Authorization": "valid_token"}
		invalidAuth := map[string]string{"Authorization": "invalid_token"}

		ts.Run(t, []test.TestCase{
			{Path: "/test-api/", Code: http.StatusOK, Headers: validAuth},
			{Path: "/test-api/", Code: http.StatusForbidden, Headers: invalidAuth},
			{Path: "/test-api/", Code: http.StatusForbidden, Headers: validAuth},
		}...)
	})

	t.Run("Auth with policy", func(t *testing.T) {
		gateway.BuildAndLoadAPI(func(spec *gateway.APISpec) {
			spec.Proxy.ListenPath = "/test-api/"
			spec.UseKeylessAccess = false
			spec.EnableCoProcessAuth = true
			spec.CustomMiddlewareBundle = authCheckBundle
			spec.VersionData.NotVersioned = true
		})

		time.Sleep(1 * time.Second)

		pID := gateway.CreatePolicy(func(p *user.Policy) {
			p.QuotaMax = 1
			p.QuotaRenewalRate = 60
		})

		policyAuth := map[string]string{"Authorization": "policy", "Policy": pID}

		ts.Run(t, []test.TestCase{
			{Path: "/test-api/", Code: http.StatusOK, Headers: policyAuth},
			{Path: "/test-api/", Code: http.StatusForbidden, Headers: policyAuth},
		}...)
	})

	t.Run("Single-file bundle with post hook", func(t *testing.T) {

		keyID := gateway.CreateSession(func(s *user.SessionState) {
			s.MetaData = map[string]interface{}{
				"testkey":   map[string]interface{}{"nestedkey": "nestedvalue"},
				"stringkey": "testvalue",
			}
		})

		gateway.BuildAndLoadAPI(func(spec *gateway.APISpec) {
			spec.Proxy.ListenPath = "/test-api-2/"
			spec.UseKeylessAccess = false
			spec.EnableCoProcessAuth = false
			spec.CustomMiddlewareBundle = postHookBundle
			spec.VersionData.NotVersioned = true
		})

		time.Sleep(1 * time.Second)

		auth := map[string]string{"Authorization": keyID}

		ts.Run(t, []test.TestCase{
			{Path: "/test-api-2/", Code: http.StatusOK, Headers: auth},
		}...)
	})

	t.Run("Single-file bundle with pre hook and UTF-8/non-UTF-8 request data", func(t *testing.T) {
		gateway.BuildAndLoadAPI(func(spec *gateway.APISpec) {
			spec.Proxy.ListenPath = "/test-api-2/"
			spec.UseKeylessAccess = true
			spec.EnableCoProcessAuth = false
			spec.CustomMiddlewareBundle = preHookBundle
			spec.VersionData.NotVersioned = true
		})

		time.Sleep(1 * time.Second)

		fileData := gateway.GenerateTestBinaryData()
		var buf bytes.Buffer
		multipartWriter := multipart.NewWriter(&buf)
		file, err := multipartWriter.CreateFormFile("file", "test.bin")
		if err != nil {
			t.Fatalf("Couldn't use multipart writer: %s", err.Error())
		}
		_, err = fileData.WriteTo(file)
		if err != nil {
			t.Fatalf("Couldn't write to multipart file: %s", err.Error())
		}
		field, err := multipartWriter.CreateFormField("testfield")
		if err != nil {
			t.Fatalf("Couldn't use multipart writer: %s", err.Error())
		}
		_, err = field.Write([]byte("testvalue"))
		if err != nil {
			t.Fatalf("Couldn't write to form field: %s", err.Error())
		}
		err = multipartWriter.Close()
		if err != nil {
			t.Fatalf("Couldn't close multipart writer: %s", err.Error())
		}

		ts.Run(t, []test.TestCase{
			{Path: "/test-api-2/", Code: http.StatusOK, Data: &buf, Headers: map[string]string{"Content-Type": multipartWriter.FormDataContentType()}},
			{Path: "/test-api-2/", Code: http.StatusOK, Data: "{}", Headers: map[string]string{"Content-Type": "application/json"}},
		}...)
	})

	t.Run("python post hook with url rewrite and method transform", func(t *testing.T) {
		gateway.BuildAndLoadAPI(func(spec *gateway.APISpec) {
			spec.Proxy.ListenPath = "/test-api-1/"
			spec.UseKeylessAccess = true
			spec.EnableCoProcessAuth = false
			spec.CustomMiddlewareBundle = postRequestTransformHookBundle

			v1 := spec.VersionData.Versions["v1"]
			v1.UseExtendedPaths = true
			v1.ExtendedPaths.URLRewrite = []apidef.URLRewriteMeta{{
				Path:         "/get",
				Method:       http.MethodGet,
				MatchPattern: "/get",
				RewriteTo:    "/test2",
			}}

			v1.ExtendedPaths.MethodTransforms = []apidef.MethodTransformMeta{{
				Path:     "/get",
				Method:   http.MethodGet,
				ToMethod: http.MethodPost,
			}}

			spec.VersionData.Versions["v1"] = v1

		}, func(spec *gateway.APISpec) {
			spec.Name = "test-api-2"
			spec.Proxy.ListenPath = "/test-api-2/"
			spec.UseKeylessAccess = true
			spec.EnableCoProcessAuth = false
			spec.UseKeylessAccess = true
		})

		time.Sleep(1 * time.Second)

		ts.Run(t, []test.TestCase{
			{Path: "/test-api-1/get", Code: http.StatusOK, BodyMatch: "newpath"},
			{Path: "/test-api-1/get", Code: http.StatusOK, BodyMatch: "GET"},
			{Path: "/test-api-1/post", Code: http.StatusOK, BodyNotMatch: "newpath"},
		}...)
	})
}
