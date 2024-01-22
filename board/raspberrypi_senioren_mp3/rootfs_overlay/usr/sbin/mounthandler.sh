#!/bin/sh
# Based on http://www.armadeus.org/wiki/index.php?title=Automatically_mount_removable_media

destdir=/run/media

my_umount()
{
	if grep -qs "^/dev/$1 " /proc/mounts ; then
		umount -l "${destdir}/$1";
	fi

	[ -d "${destdir}/$1" ] && rmdir "${destdir}/$1"
}

my_mount()
{
	mkdir -p "${destdir}/$1" || exit 1

	if ! mount -o nodev,ro "/dev/$1" "${destdir}/$1"; then
		# failed to mount, clean up mountpoint
		rmdir "${destdir}/$1"
		exit 1
	fi
}

case "${ACTION}" in
add|"")
	my_umount ${MDEV}
	my_mount ${MDEV}
	;;
remove)
	my_umount ${MDEV}
	;;
esac

# signal application
kill -WINCH $(pidof go-mp3player)
