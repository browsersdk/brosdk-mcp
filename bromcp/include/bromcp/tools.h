#pragma once

#include "bromcp/brosdk_client.h"
#include "bromcp/task_store.h"
#include "bromcp/tool_registry.h"

namespace bromcp {

void RegisterBroTools(ToolRegistry &registry, BrosdkClient &client,
                      TaskStore &tasks);

} // namespace bromcp
