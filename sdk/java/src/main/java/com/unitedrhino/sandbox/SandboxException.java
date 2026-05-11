package com.unitedrhino.sandbox;

public class SandboxException extends RuntimeException {
    private final int statusCode;

    public SandboxException(int statusCode, String message) {
        super("[" + statusCode + "] " + message);
        this.statusCode = statusCode;
    }

    public int getStatusCode() {
        return statusCode;
    }
}
