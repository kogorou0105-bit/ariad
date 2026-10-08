import { createRootRoute, createRoute, createRouter } from "@tanstack/react-router";

import { AdministratorsPage, ConsoleShell, ConversationDetailPage, ConversationsPage, KnowledgeSourceDetailPage, KnowledgeSourcesPage, ModelConfigPage, OverviewPage, PlaygroundPage, ReviewDetailPage, ReviewQueuePage, VisitorsPage } from "./App";

const rootRoute = createRootRoute({ component: ConsoleShell });
const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/",
  component: OverviewPage,
});
const conversationsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/conversations",
  validateSearch: (search: Record<string, unknown>) => ({ visitorId: typeof search.visitorId === "string" ? search.visitorId : "" }),
  component: ConversationsPage,
});
const visitorsRoute = createRoute({ getParentRoute: () => rootRoute, path: "/visitors", component: VisitorsPage });
const knowledgeRoute = createRoute({ getParentRoute: () => rootRoute, path: "/knowledge", component: KnowledgeSourcesPage });
const knowledgeDetailRoute = createRoute({ getParentRoute: () => rootRoute, path: "/knowledge/$sourceId", component: KnowledgeSourceDetailPage });
const playgroundRoute = createRoute({ getParentRoute: () => rootRoute, path: "/playground", component: PlaygroundPage });
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
const administratorsRoute = createRoute({ getParentRoute: () => rootRoute, path: "/settings/administrators", component: AdministratorsPage });
const routeTree = rootRoute.addChildren([indexRoute, knowledgeRoute, knowledgeDetailRoute, playgroundRoute, conversationsRoute, conversationDetailRoute, visitorsRoute, reviewsRoute, reviewDetailRoute, modelConfigRoute, administratorsRoute]);

export const router = createRouter({ routeTree });

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
