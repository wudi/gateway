package sdk

const typescriptTemplate = `// {{.Title}} API Client (auto-generated)

export interface ApiResponse {
  status: number;
  body: any;
  headers: Headers;
}

export class Client {
  private baseUrl: string;
  private headers: Record<string, string>;

  constructor(baseUrl: string, headers: Record<string, string> = {}) {
    this.baseUrl = baseUrl.replace(/\/+$/, "");
    this.headers = headers;
  }

  private async request(method: string, path: string, body?: any, params?: Record<string, string>): Promise<ApiResponse> {
    let url = this.baseUrl + path;
    if (params) {
      const qs = new URLSearchParams(params).toString();
      if (qs) url += "?" + qs;
    }

    const init: RequestInit = {
      method,
      headers: {
        ...this.headers,
        ...(body ? { "Content-Type": "application/json" } : {}),
      },
    };
    if (body) {
      init.body = JSON.stringify(body);
    }

    const resp = await fetch(url, init);
    const respBody = await resp.json().catch(() => null);

    return {
      status: resp.status,
      body: respBody,
      headers: resp.headers,
    };
  }

{{range .Endpoints}}
  /** {{.Method}} {{.Path}}{{if .Summary}} - {{.Summary}}{{end}} */
  async {{.OperationID}}({{range $i, $p := .PathParams}}{{if $i}}, {{end}}{{$p.Name}}: {{TSParamType $p.Type}}{{end}}{{if and .PathParams .HasBody}}, {{end}}{{if .HasBody}}body: any{{end}}{{if and (or .PathParams .HasBody) .QueryParams}}, {{end}}{{range $i, $p := .QueryParams}}{{if $i}}, {{end}}{{$p.Name}}?: {{TSParamType $p.Type}}{{end}}): Promise<ApiResponse> {
    const path = ` + "`" + `{{FormatTSPath .Path}}` + "`" + `;
    {{- if .QueryParams}}
    const params: Record<string, string> = {};
    {{- range .QueryParams}}
    if ({{.Name}} !== undefined) params["{{.Name}}"] = String({{.Name}});
    {{- end}}
    return this.request("{{.Method}}", path, {{if .HasBody}}body{{else}}undefined{{end}}, params);
    {{- else}}
    return this.request("{{.Method}}", path{{if .HasBody}}, body{{end}});
    {{- end}}
  }
{{end}}
}
`
