# Distributed Fine-Tuning of BERT LLM for Sentiment Analysis with Kubeflow PyTorchJob

This notebook instructs how to refine a BERT language model (LLM) for analyzing text sentiment (positive/negative) using the Kubeflow PyTorchJob framework and a distributed training approach.

### **Key Technologies:**

Python libraries and dependencies:
*   **Hugging Face Transformers:** Popular library providing pre-trained models (like BERT) and tools for working with them.
*   **Datasets library:** Efficiently loads and processes the Yelp review dataset. 
*   **AWS S3 (or MinIO):** Object storage library for saving the fine-tuned model.
*   **BERT:** (Bidirectional Encoder Representations from Transformers) A state-of-the-art language model used for various natural language processing tasks.

For the infrastructure:
1.  **OpenEBS:** A software-defined storage solution for Kubernetes 
    * to handle the storage requests from minio and jupyter servers(per user).
2.  **MinIO:** An object storage system compatible with the AWS S3 API for storing models and data.
3.  **JupyterHub:** A multi-user platform for interactive Jupyter Notebooks, a common tool for data science and machine learning development.

4.   **Kubeflow:** A platform for managing machine learning workflows on Kubernetes.
        * **Kubeflow Training Operator:** A Kubernetes component for managing the training of machine learning models.
        * **PyTorchJob:** A Kubeflow (-Kubernetes) CRD for running PyTorch distributed training jobs.


**Step-by-Step Setting up the infrastracture**
> given that we already have an HPK instance running

> there is a install.sh script that does all the following

1.  **Install OpenEBS**

2.  **Install MinIO**

3.  **Install Kubeflow Training Operator:**
    *   Uses `kubectl apply` to install the Kubeflow Training Operator from its official GitHub repository (version 1.7.0).

4.  **Install JupyterHub:**
    *   Creates the `kubeflow` namespace. (!!)
    *   Employs Helm to install (or upgrade) a JupyterHub instance named `my-jupyter`.

<!-- 5.  **Get MinIO Credentials:**
    *   Retrieves the access key and secret key for the MinIO instance from a  Kubernetes secret named "myminio".
    *   Displays these credentials to use them inside the notebook 

6.  **Download and Configure MinIO Client (`mc`):**
    *  Retrieves the internal MinIO service IP address (`ENDPOINT`) 
    *  Sets up an `mc` alias named 'local' targeting the MinIO instance and using the retrieved credentials. -->

<!-- 7.  **Create MinIO Bucket:**
    *   Creates a bucket named 'kubeflow-examples' in MinIO. -->

> also, modify the /etc/apptainer/apptainer.conf file, to increase the overlay storage to something like 32GB on the `sessiondir max size` field

To create an alias for MinIO:
> kubectl port-forward svc/myminio -n minio 9000:9000

then: 
`MC_ACCESS_KEY=$(kubectl get secret myminio -n minio -o jsonpath="{.data.rootUser}" | base64 --decode)` 

and:  
`MC_SECRET_KEY=$(kubectl get secret myminio -n minio -o jsonpath="{.data.rootPassword}" | base64 --decode)`

and:
`./mc alias set local http://localhost:9000 $MC_ACCESS_KEY $MC_SECRET_KEY`

and finally, create the bucket:
`mc mb local/kubeflow-examples`.

## **Notebook Structure**

1. **Import Libraries:** Load necessary libraries for model fine-tuning, data processing, distributed training, and S3 interaction.
2. **Load Yelp Sample Data:** Fetch a subset of the Yelp reviews dataset from Hugging Face. 
3. **Create Fine-Tuning Script:**  Define the `train_func` function, which encapsulates these core steps:
    1. Download BERT model and tokenizer
    2. Download Yelp dataset
    3. Tokenize the text data for BERT
    4. Distribute dataset across workers
    5. Set up training arguments
    6. Create the PyTorch Trainer
    7. Fine-tune BERT
    8. Worker with RANK=0 saves the model to S3  

4. **Create PyTorchJob:**  Instantiate the PyTorchJob using the training function, specifying details like:
    Number of workers (GPUs)
    Resource allocation per worker
    AWS S3 Bucket
5. **Monitor Job:**  Check job status and wait for completion.
6. **Get Logs:**  Fetch training logs from the worker pods.
7. **Download Model:**  Retrieve the fine-tuned model from S3.
8. **Test Model:**  Load the model and use the Hugging Face pipeline for sentiment analysis on sample text.

**Details and Considerations**

*   **Customization:**  The `train_func` could be modified with more samples from Yelp dataset to further adjust the training time.



**Key Concepts**

* **Distributed Data Parallel (DDP):** The core strategy used to train the model across multiple workers (optionally with multiple GPUs). Each worker gets a portion of the training data and maintains a local copy of the BERT model.  
* **PyTorch Trainer:** The Pytorch `Trainer` handles much of the distribution complexity behind the scenes.
* **Synchronization:** During training, workers need to periodically communicate and synchronize their model updates (gradients) to maintain consistency. 

## **How Distribution Works (Simplified)**

1.  **Dataset Splitting:** 
    *   The full Yelp dataset is partitioned into smaller chunks.
    *   The `split_dataset_by_node` function ensures each worker receives a unique, roughly equal portion of the data.

2.  **Model Initialization:** Each worker initializes its own local copy of the BERT model. These local copies start with identical weights (parameters).

3.  **Training Loop:**

    *   **Forward Pass:** Each worker processes its assigned data batch through its local BERT model.
    *   **Loss Calculation:** Each worker computes the training loss (how well the model performed on its data batch).
    *   **Backward Pass:** Each worker calculates gradients based on the loss. Gradients represent how the model's weights should change to improve performance.  
    *   **Gradient Synchronization:**  Workers communicate and collectively average their gradients. This ensures a consistent view of model updates across all workers.
    *   **Model Update:** Each worker applies the averaged gradients to update its local BERT model.

4.  **Repeating the Loop:**  The process repeats for multiple iterations (epochs) over the data.

## Technical Difficulties

- Apptainer, by default, has a tiny overlay for read-write operations, so for jupyter server to download all the packages from Python, it needs a couple of gigabytes to store the libraries and the model.

- Jupyterhub pings on its proxy server periodically so i needed to supply the full AAAAA hostname