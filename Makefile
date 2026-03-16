BINARY := slack-agent-bridge
AGENT ?= $(error AGENT is required for install/uninstall, e.g. make install AGENT=myagent)
PLIST_LABEL := com.slack-agent-bridge.$(AGENT)
PLIST_DIR := $(HOME)/Library/LaunchAgents
PLIST_PATH := $(PLIST_DIR)/$(PLIST_LABEL).plist

.PHONY: build install uninstall logs

build:
	go build -o $(BINARY) .

install: build
	@echo "Installing $(PLIST_LABEL)..."
	@mkdir -p $(PLIST_DIR)
	@sed -e 's|{{AGENT_NAME}}|$(AGENT)|g' \
		-e 's|{{BINARY_PATH}}|$(CURDIR)/$(BINARY)|g' \
		-e 's|{{WORKING_DIR}}|$(CURDIR)|g' \
		-e 's|{{LOG_DIR}}|$(CURDIR)/logs|g' \
		-e 's|{{PLIST_LABEL}}|$(PLIST_LABEL)|g' \
		examples/launchd.plist.tmpl > $(PLIST_PATH)
	@mkdir -p $(CURDIR)/logs
	launchctl load $(PLIST_PATH)
	@echo "Installed and started $(PLIST_LABEL)"

uninstall:
	@echo "Uninstalling $(PLIST_LABEL)..."
	-launchctl unload $(PLIST_PATH) 2>/dev/null
	-rm -f $(PLIST_PATH)
	@echo "Uninstalled $(PLIST_LABEL)"

logs:
	@tail -f logs/*.log