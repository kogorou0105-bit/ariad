import { createRootRoute, createRoute, createRouter } from "@tanstack/react-router";

import { ConsoleShell, ConversationDetailPage, ConversationsPage, ModelConfigPage, OverviewPage, ReviewDetailPage, ReviewQueuePage } from "./App";

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
const reviewsRoute = createRoute({ getParentRoute: () => rootRoute, path: "/reviews", component: ReviewQueuePage });
const reviewDetailRoute = createRoute({ getParentRoute: () => rootRoute, path: "/reviews/$conversationId", validateSearch: (search: Record<string, unknown>) => ({ visitorId: typeof search.visitorId === "string" ? search.visitorId : "" }), component: ReviewDetailPage });
const routeTree = rootRoute.addChildren([indexRoute, conversationsRoute, conversationDetailRoute, reviewsRoute, reviewDetailRoute, modelConfigRoute]);

export const router = createRouter({ routeTree });

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
