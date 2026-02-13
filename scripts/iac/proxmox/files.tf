# Files already exist, skip

#resource "proxmox_virtual_environment_download_file" "debian_cloud_image_neon" {
#  content_type = "iso"
#  datastore_id = "local"
#  node_name    = "neon"
#  url          = "https://cdimage.debian.org/images/cloud/bookworm/20240211-1654/debian-12-genericcloud-amd64-20240211-1654.qcow2"
#  file_name    = "debian-12-genericcloud-amd64-20240211-1654.img"
#}

#resource "proxmox_virtual_environment_download_file" "debian_cloud_image_radon" {
#  content_type = "iso"
#  datastore_id = "local"
#  node_name    = "radon"
#  url          = "https://cdimage.debian.org/images/cloud/bookworm/20240211-1654/debian-12-genericcloud-amd64-20240211-1654.qcow2"
#  file_name    = "debian-12-genericcloud-amd64-20240211-1654.img"
#}