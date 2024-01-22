################################################################################
#
# go-mp3player
#
################################################################################

# GO_MP3PLAYER_SITE = $(GO_MP3PLAYER_PKGDIR)/src

GO_MP3PLAYER_GO_ENV = GO111MODULE=

define GO_MP3PLAYER_CONFIGURE_CMDS
endef

$(eval $(golang-package))

GO_MP3PLAYER_SRC_PATH = $(GO_MP3PLAYER_PKGDIR)/src

# $(info DEBUG [$(GO_MP3PLAYER_SRC_PATH)])
