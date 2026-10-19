import { Link } from "react-router-dom";
import type { Project } from "../../lib/api";
import { STAGE_ITEMS, stagePath, stageState } from "./stages";

export function ProjectStageNav({ project }: { project: Project }) {
  return (
    <nav aria-label="制作阶段">
      <ol className="ui-stages project-stage-nav">
        {STAGE_ITEMS.map((item, index) => {
          const state = stageState(project, item.key);
          const content = (
            <>
              <span className={state === "current" ? "ui-stage-active" : ""}>
                {index + 1}
              </span>
              <b>{item.label}</b>
            </>
          );
          return (
            <li
              key={item.key}
              className={
                state === "current"
                  ? "is-current"
                  : state === "done"
                    ? "is-done"
                    : "is-locked"
              }
            >
              {state === "locked" ? (
                <span
                  className="project-stage-locked"
                  aria-disabled="true"
                  title="确认上一环节后才能进入"
                >
                  {content}
                </span>
              ) : (
                <Link
                  to={stagePath(project.id, item.key)}
                  aria-current={state === "current" ? "step" : undefined}
                >
                  {content}
                </Link>
              )}
            </li>
          );
        })}
      </ol>
    </nav>
  );
}
