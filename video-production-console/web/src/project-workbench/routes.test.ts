import { describe, expect, test } from "vitest";
import { parseLocation } from "./routes";

const projectID = "123e4567-e89b-12d3-a456-426614174000";

describe("application routes", () => {
  test("parses each production page into an explicit route", () => {
    expect(parseLocation("/")).toEqual({ view: "remix-lab" });
    expect(parseLocation("/projects")).toEqual({ view: "projects" });
    expect(parseLocation(`/projects/${projectID}`)).toEqual({
      view: "project",
      projectID,
    });
    expect(parseLocation("/image-projects")).toEqual({ view: "image-projects" });
    expect(parseLocation("/image-projects/advanced")).toEqual({ view: "image-projects-advanced" });
    expect(parseLocation("/image-projects/advanced/")).toEqual({ view: "image-projects-advanced" });
    expect(parseLocation(`/image-projects/${projectID}`)).toEqual({
      view: "image-project",
      projectID,
    });
    expect(parseLocation("/image-videos")).toEqual({ view: "image-videos" });
    expect(parseLocation(`/image-videos/${projectID}`)).toEqual({
      view: "image-video",
      projectID,
    });
    expect(parseLocation("/remix-lab")).toEqual({ view: "remix-lab" });
    expect(parseLocation("/remix-lab/")).toEqual({ view: "remix-lab" });
    expect(parseLocation(`/remix-lab/${projectID}`)).toEqual({
      view: "remix-lab",
      experimentID: projectID,
    });
  });

  test("rejects malformed and extra route segments", () => {
    for (const pathname of [
      "/unknown",
      "/projects/not-a-uuid",
      `/projects/${projectID}/extra`,
      "/image-projects/not-a-uuid",
      `/image-projects/${projectID}/extra`,
      "/image-videos/not-a-uuid",
      `/image-videos/${projectID}/extra`,
      "/remix-lab/not-a-uuid",
      `/remix-lab/${projectID}/extra`,
    ]) {
      expect(parseLocation(pathname)).toEqual({ view: "not-found" });
    }
  });
});
