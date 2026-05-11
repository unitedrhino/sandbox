package com.unitedrhino.sandbox;

import com.fasterxml.jackson.core.type.TypeReference;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.unitedrhino.sandbox.model.*;

import java.io.BufferedReader;
import java.io.IOException;
import java.io.InputStreamReader;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.time.Duration;
import java.util.List;
import java.util.Map;
import java.util.function.Consumer;

/**
 * Sandbox HTTP API Java SDK（需要 Java 11+）
 *
 * <p>依赖: com.fasterxml.jackson.core:jackson-databind</p>
 */
public class SandboxClient {
    private final String baseUrl;
    private final HttpClient httpClient;
    private final ObjectMapper objectMapper;

    public SandboxClient(String baseUrl) {
        this(baseUrl, HttpClient.newBuilder()
                .connectTimeout(Duration.ofSeconds(30))
                .build());
    }

    public SandboxClient(String baseUrl, HttpClient httpClient) {
        this.baseUrl = baseUrl.endsWith("/") ? baseUrl.substring(0, baseUrl.length() - 1) : baseUrl;
        this.httpClient = httpClient;
        this.objectMapper = new ObjectMapper();
    }

    private String url(String path) {
        return baseUrl + path;
    }

    private HttpRequest.Builder request(String method, String path) {
        return HttpRequest.newBuilder()
                .uri(URI.create(url(path)))
                .timeout(Duration.ofSeconds(30))
                .method(method, HttpRequest.BodyPublishers.noBody());
    }

    private HttpRequest.Builder requestJson(String method, String path, Object body) throws IOException {
        byte[] json = objectMapper.writeValueAsBytes(body);
        return HttpRequest.newBuilder()
                .uri(URI.create(url(path)))
                .timeout(Duration.ofSeconds(30))
                .header("Content-Type", "application/json")
                .method(method, HttpRequest.BodyPublishers.ofByteArray(json));
    }

    private <T> T parse(HttpResponse<String> resp, Class<T> clazz) throws IOException {
        if (resp.statusCode() >= 400) {
            throw new SandboxException(resp.statusCode(), resp.body());
        }
        return objectMapper.readValue(resp.body(), clazz);
    }

    private <T> T parse(HttpResponse<String> resp, TypeReference<T> typeRef) throws IOException {
        if (resp.statusCode() >= 400) {
            throw new SandboxException(resp.statusCode(), resp.body());
        }
        return objectMapper.readValue(resp.body(), typeRef);
    }

    /** 检查服务健康状态 */
    public Map<String, Object> healthz() throws IOException, InterruptedException {
        HttpResponse<String> resp = httpClient.send(
                request("GET", "/healthz").build(),
                HttpResponse.BodyHandlers.ofString());
        return parse(resp, new TypeReference<Map<String, Object>>() {});
    }

    /** 检查服务就绪状态 */
    public Map<String, Object> readyz() throws IOException, InterruptedException {
        HttpResponse<String> resp = httpClient.send(
                request("GET", "/readyz").build(),
                HttpResponse.BodyHandlers.ofString());
        return parse(resp, new TypeReference<Map<String, Object>>() {});
    }

    /** 获取运行时状态 */
    public StatusPayload getStatus() throws IOException, InterruptedException {
        HttpResponse<String> resp = httpClient.send(
                request("GET", "/runtime/status").build(),
                HttpResponse.BodyHandlers.ofString());
        return parse(resp, StatusPayload.class);
    }

    /** 列出所有 Skill */
    public List<SkillInfo> listSkills() throws IOException, InterruptedException {
        HttpResponse<String> resp = httpClient.send(
                request("GET", "/runtime/skills").build(),
                HttpResponse.BodyHandlers.ofString());
        return parse(resp, new TypeReference<List<SkillInfo>>() {});
    }

    /** 重新加载 Skill 列表 */
    public Map<String, Object> reloadSkills() throws IOException, InterruptedException {
        HttpResponse<String> resp = httpClient.send(
                request("POST", "/runtime/skills/reload").build(),
                HttpResponse.BodyHandlers.ofString());
        return parse(resp, new TypeReference<Map<String, Object>>() {});
    }

    /** 获取指定 Skill 信息 */
    public SkillInfo getSkill(String name) throws IOException, InterruptedException {
        HttpResponse<String> resp = httpClient.send(
                request("GET", "/runtime/skills/" + name).build(),
                HttpResponse.BodyHandlers.ofString());
        return parse(resp, SkillInfo.class);
    }

    /** 激活指定 Skill 的指定版本 */
    public Map<String, Object> activateSkill(String name, String version) throws IOException, InterruptedException {
        Map<String, String> body = Map.of("version", version);
        HttpResponse<String> resp = httpClient.send(
                requestJson("POST", "/runtime/skills/" + name + "/activate", body).build(),
                HttpResponse.BodyHandlers.ofString());
        return parse(resp, new TypeReference<Map<String, Object>>() {});
    }

    /** 启动运行时 */
    public Map<String, Object> startRuntime() throws IOException, InterruptedException {
        HttpResponse<String> resp = httpClient.send(
                request("POST", "/runtime/start").build(),
                HttpResponse.BodyHandlers.ofString());
        return parse(resp, new TypeReference<Map<String, Object>>() {});
    }

    /** 停止运行时 */
    public Map<String, Object> stopRuntime() throws IOException, InterruptedException {
        HttpResponse<String> resp = httpClient.send(
                request("POST", "/runtime/stop").build(),
                HttpResponse.BodyHandlers.ofString());
        return parse(resp, new TypeReference<Map<String, Object>>() {});
    }

    /** 同步执行命令 */
    public ExecResult exec(List<String> command, Map<String, String> env, int timeoutSeconds) throws IOException, InterruptedException {
        Map<String, Object> body = new java.util.HashMap<>();
        body.put("command", command);
        if (env != null) body.put("env", env);
        if (timeoutSeconds > 0) body.put("timeoutSeconds", timeoutSeconds);
        HttpResponse<String> resp = httpClient.send(
                requestJson("POST", "/runtime/exec", body).build(),
                HttpResponse.BodyHandlers.ofString());
        return parse(resp, ExecResult.class);
    }

    /** 列出所有任务 */
    public List<Task> listTasks() throws IOException, InterruptedException {
        HttpResponse<String> resp = httpClient.send(
                request("GET", "/runtime/tasks").build(),
                HttpResponse.BodyHandlers.ofString());
        return parse(resp, new TypeReference<List<Task>>() {});
    }

    /** 提交异步任务 */
    public Task submitTask(List<String> command, Map<String, String> env, int timeoutSeconds) throws IOException, InterruptedException {
        Map<String, Object> body = new java.util.HashMap<>();
        body.put("command", command);
        if (env != null) body.put("env", env);
        if (timeoutSeconds > 0) body.put("timeoutSeconds", timeoutSeconds);
        HttpResponse<String> resp = httpClient.send(
                requestJson("POST", "/runtime/tasks", body).build(),
                HttpResponse.BodyHandlers.ofString());
        return parse(resp, Task.class);
    }

    /** 获取任务详情 */
    public Task getTask(String taskId) throws IOException, InterruptedException {
        HttpResponse<String> resp = httpClient.send(
                request("GET", "/runtime/tasks/" + taskId).build(),
                HttpResponse.BodyHandlers.ofString());
        return parse(resp, Task.class);
    }

    /** 取消任务 */
    public Task cancelTask(String taskId) throws IOException, InterruptedException {
        HttpResponse<String> resp = httpClient.send(
                request("POST", "/runtime/tasks/" + taskId + "/cancel").build(),
                HttpResponse.BodyHandlers.ofString());
        return parse(resp, Task.class);
    }

    /** 订阅 SSE 事件流 */
    public void subscribeEvents(Consumer<Event> onEvent) throws IOException, InterruptedException {
        HttpRequest req = HttpRequest.newBuilder()
                .uri(URI.create(url("/runtime/stream")))
                .GET()
                .build();
        HttpResponse<java.io.InputStream> resp = httpClient.send(req, HttpResponse.BodyHandlers.ofInputStream());
        if (resp.statusCode() >= 400) {
            String body = new String(resp.body().readAllBytes(), java.nio.charset.StandardCharsets.UTF_8);
            throw new SandboxException(resp.statusCode(), body);
        }
        try (BufferedReader reader = new BufferedReader(new InputStreamReader(resp.body()))) {
            String line;
            while ((line = reader.readLine()) != null) {
                if (line.startsWith("data: ")) {
                    String data = line.substring(6);
                    try {
                        Event event = objectMapper.readValue(data, Event.class);
                        onEvent.accept(event);
                    } catch (IOException ignored) {
                    }
                }
            }
        }
    }
}
