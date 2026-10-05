import { createRootRoute, createRoute, createRouter } from "@tanstack/react-router";

import { ConsoleShell, OverviewPage } from "./App";

const rootRoute = createRootRoute({ component: ConsoleShell });
const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/",
  component: OverviewPage,
});
const routeTree = rootRoute.addChildren([indexRoute]);

export const router = createRouter({ routeTree });

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
