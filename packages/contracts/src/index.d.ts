/** The browser surface consuming a shared transport contract. */
export type AppSurface = "console" | "widget";

/** Minimal API health response shared by browser applications. */
export interface HealthStatus {
  status: "ok";
  service: "api";
}
