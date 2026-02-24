package sdk

const pythonTemplate = `"""{{.Title}} API Client (auto-generated)"""
import requests
from dataclasses import dataclass
from typing import Any, Dict, Optional


@dataclass
class Response:
    status_code: int
    body: bytes
    headers: Dict[str, str]
    json_data: Any = None

    def json(self) -> Any:
        if self.json_data is None:
            import json
            self.json_data = json.loads(self.body)
        return self.json_data


class Client:
    """{{.Title}} API client."""

    def __init__(self, base_url: str, headers: Optional[Dict[str, str]] = None):
        self.base_url = base_url.rstrip("/")
        self.session = requests.Session()
        if headers:
            self.session.headers.update(headers)

    def _request(self, method: str, path: str, json_body: Any = None, params: Optional[Dict] = None) -> Response:
        url = self.base_url + path
        resp = self.session.request(method, url, json=json_body, params=params)
        return Response(
            status_code=resp.status_code,
            body=resp.content,
            headers=dict(resp.headers),
        )

{{range .Endpoints}}
    def {{SnakeCase .OperationID}}(self{{range .PathParams}}, {{.Name}}: {{PythonParamType .Type}}{{end}}{{if .HasBody}}, body: Any = None{{end}}{{range .QueryParams}}, {{.Name}}: {{PythonParamType .Type}} = None{{end}}) -> Response:
        """{{.Method}} {{.Path}}{{if .Summary}} - {{.Summary}}{{end}}"""
        path = f"{{FormatPythonPath .Path}}"
        {{- if .QueryParams}}
        params = {}
        {{- range .QueryParams}}
        if {{.Name}} is not None:
            params["{{.Name}}"] = {{.Name}}
        {{- end}}
        return self._request("{{.Method}}", path{{if .HasBody}}, json_body=body{{else}}, json_body=None{{end}}, params=params)
        {{- else}}
        return self._request("{{.Method}}", path{{if .HasBody}}, json_body=body{{end}})
        {{- end}}
{{end}}
`
