import { useEffect } from "react";
import { useNodesInitialized, useReactFlow, useStore } from "@xyflow/react";

/** Keep the starting input in view without changing any saved node positions. */
export function InitialFlowViewport({ token }: { token: string }) {
  const initialized = useNodesInitialized();
  const width = useStore(state => state.width);
  const height = useStore(state => state.height);
  const { getNodes, fitView } = useReactFlow();
  useEffect(() => {
    if (!initialized || !width || !height || !token) return;
    const timer = window.setTimeout(() => {
      const nodes = getNodes().filter(node => !node.id.startsWith("produce-"));
      const input = nodes.find(node => node.id === "source") || nodes[0];
      if (!input) return;
      const nearby = nodes.filter(node => node.id !== input.id)
        .sort((a, b) => Math.hypot(a.position.x - input.position.x, a.position.y - input.position.y) - Math.hypot(b.position.x - input.position.x, b.position.y - input.position.y));
      // Narrow panes show the input at reading size; users pan or fit the full graph explicitly.
      const focus = width < 720 ? [input] : [input, ...nearby.slice(0, 2)];
      void fitView({ nodes: focus, padding: 0.2, minZoom: 0.1, maxZoom: 1, duration: 0 });
    }, 80);
    return () => window.clearTimeout(timer);
  }, [initialized, width, height, token, getNodes, fitView]);
  return null;
}
