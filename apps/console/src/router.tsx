import { createRootRoute, createRoute, createRouter } from "@tanstack/react-router";

import { ConsoleShell, ConversationDetailPage, ConversationsPage, ModelConfigPage, OverviewPage } from "./App";

const rootRoute = createRootRoute({ component: ConsoleShell });
const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/",
  component: OverviewPage,
});
const conversationsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/conversations",
  component: ConversationsPage,
});
const conversationDetailRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/conversations/$conversationId",
  validateSearch: (search: Record<string, unknown>) => ({
    visitorId: typeof search.visitorId === "string" ? search.visitorId : "",
  }),
  component: ConversationDetailPage,
});
const modelConfigRoute = createRoute({ getParentRoute: () => rootRoute, path: "/settings/model", component: ModelConfigPage });
const routeTree = rootRoute.addChildren([indexRoute, conversationsRoute, conversationDetailRoute, modelConfigRoute]);

export const router = createRouter({ routeTree });

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
